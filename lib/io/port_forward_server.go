package io

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
	"gopkg.in/tomb.v2"
	"io"
	"net"
	"strconv"
	"strings"
)

type PortForwardSignalType int

const (
	SignalHandShake = iota
	SignalConnect
	SignalConnectRefuse
	SignalConnectAgree
	SignalBye
)

type PortForwardSignalCode int

const (
	CodeForbidden = iota
	CodePortRefused
	CodePortTimeout
)

type ForwardPortServerConfig struct {
	DestHost string
	DestPort uint16
}

func (config ForwardPortServerConfig) ForwardPortServer(ctx context.Context, commonSession pairconnect.MuxSession) error {
	var err error
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	defer func() {
		t.Kill(nil)
		t.Wait()
	}()
	//go func() {
	//	// handle signal goroutine
	//	select {
	//	case <- signal.SignalSIGINT:
	//		t.Kill(nil)
	//	case <- t.Dead():
	//	}
	//}()
	commandStream, err := commonSession.Accept()
	if err != nil {
		log.Errorf("cannot accept stream on common session")
		return err
	}
	defer commandStream.Close()
	// confirm each side identity
	{
		n, err := commandStream.Write([]byte(ModePortForwardServer))
		if err != nil || n != ModeStringLen {
			log.Error("unable to send identity")
			return errors.New("unable to send identity")
		}
		var buf = make([]byte, ModeStringLen)
		_, err = commandStream.Read(buf)
		if err != nil || !bytes.Equal(buf, []byte(ModePortForwardClient)) {
			log.Error("unable to confirm peer identity")
			return errors.New("unable to confirm peer identity")
		}
		log.Debug("identity confirmed")
	}
	var commandReader = bufio.NewReader(commandStream)
	var commandWriter = bufio.NewWriter(commandStream)
	var cDealer = signalDealer{
		reader: commandReader,
		writer: commandWriter,
	}

	// wait for handshake
	log.Debugf("port forward server waiting for handshake")
	var commandSignal *PortForwardSignal
	commandSignal, err = cDealer.GetSignal()
	if err != nil {
		log.Error("command channel handshake failed: ", err)
		return err
	}
	if commandSignal.Version != protocolVersion || commandSignal.Type != SignalHandShake {
		log.Error("command channel handshake failed")
		return errors.New("command channel handshake failed")
	}
	if err = cDealer.SendSignal(&PortForwardSignal{
		Version: protocolVersion,
		Type:    SignalHandShake,
	}); err != nil {
		log.Error("command channel handshake failed")
		return err
	}
	log.Debugf("port forward server handshake complete")

	// command read thread
	var commandReadC = make(chan *PortForwardSignal)
	t.Go(func() error {
		defer func() {
			log.Debugf("command channel dismantling")
			close(commandReadC)
		}()
		for {
			result, err := cDealer.GetSignal()
			if err != nil {
				if err == io.EOF {
					break
				} else if strings.Contains(err.Error(), "use of closed network connection") {
					log.Debugf("read from a closed socket")
					break
				} else if strings.Contains(err.Error(), "broken pipe") {
					log.Debugf("broken pipe on command channel")
					break
				} else {
					log.Errorf("%s, %+v\n", "unexpected error", err)
					break
				}
			}
			commandReadC <- result
		}
		return nil
	})

	// main loop
	log.Infof("port forward server started")
	log.Infof("forward to %s:%d", config.DestHost, config.DestPort)
	config.PortForwardServerLoop(t, commonSession, commandReadC, cDealer)

	return nil
}

func (config ForwardPortServerConfig) PortForwardServerLoop(
	t *tomb.Tomb,
	commonSession pairconnect.MuxSession,
	commandReadC chan *PortForwardSignal,
	cDealer signalDealer,
) error {
	for {
		// detect cancellation
		select {
		case <-t.Dying():
			return nil
		default:
		}
		select {
		case <-t.Dying():
			return nil
		case commandSignal, ok := <-commandReadC:
			if !ok {
				// command socket closed
				log.Debugf("command channel closed")
				return nil
			}
			switch commandSignal.Type {
			case SignalConnect:
				// attempt to connect
				destAddr := config.DestHost + ":" + strconv.Itoa(int(config.DestPort))
				log.Infof("connecting to destination: %s", destAddr)
				conn, err := net.Dial("tcp", destAddr)
				if err != nil {
					log.Debug("connection to destination failed:", err)
					var code PortForwardSignalCode
					if err.(net.Error).Timeout() {
						code = CodePortTimeout
					} else {
						code = CodePortRefused
					}
					var toSend = PortForwardSignal{
						Version: protocolVersion,
						Type:    SignalConnectRefuse,
						Code:    code,
					}
					err = cDealer.SendSignal(&toSend)
					errorAssert(err)
					break
				}
				log.Debug("connected to destination")
				// send back the good news
				var toSend = PortForwardSignal{
					Version: protocolVersion,
					Type:    SignalConnectAgree,
				}
				err = cDealer.SendSignal(&toSend)
				errorAssert(err)
				t.Go(func() error {
					startPairing(t.Context(nil), commonSession, conn, true)
					return nil
				})
			case SignalBye:
				log.Debugf("command channel bye")
				return nil
			}
		}
	}
}

func startPairing(ctx context.Context, commonSession pairconnect.MuxSession, listenConn net.Conn, asServer bool) {
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	defer func() {
		if err := recover(); err != nil {
			panic(err)
		}
		t.Wait()
		log.Debug("pair closed")
	}()
	defer listenConn.Close()

	var conn net.Conn
	var err error
	if asServer {
		// TODO: client might send multiple requests and produce race conditions for stream, can possibly cause mismatch of connection. It is not gonna be an issue if server only serve one port per ForwardPortServer call, like now. To improve, it could use a stream identifier.
		conn, err = commonSession.Accept()
		if err != nil {
			log.Errorf("cannot accept stream on common session")
			return
		}
	} else {
		conn, err = commonSession.Open()
		if err != nil {
			log.Errorf("cannot open stream on common session")
			return
		}
	}
	defer conn.Close()

	t.Go(func() error {
		defer t.Kill(nil)
		defer conn.Close()
		defer listenConn.Close()
		ReaderToWriter(listenConn, conn)
		return nil
	})
	t.Go(func() error {
		defer t.Kill(nil)
		defer conn.Close()
		defer listenConn.Close()
		ReaderToWriter(conn, listenConn)
		return nil
	})
	<-t.Dying()
	log.Debug("pair closing")
}
