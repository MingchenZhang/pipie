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

type ForwardPortClientConfig struct {
	ListenHost string
	ListenPort uint16
}

type PortForwardClientState int

const (
	ClientStateWait = iota
	ClientStateConn1
)

func (config ForwardPortClientConfig) ForwardPortClient(ctx context.Context, commonSession pairconnect.MuxSession) error {
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
	commandStream, err := commonSession.Open()
	if err != nil {
		log.Errorf("cannot open stream on common session")
		return err
	}
	defer commandStream.Close()
	// confirm each side identity
	{
		n, err := commandStream.Write([]byte(ModePortForwardClient))
		if err != nil || n != ModeStringLen {
			log.Error("unable to send identity")
			return errors.New("unable to send identity")
		}
		var buf = make([]byte, ModeStringLen)
		_, err = commandStream.Read(buf)
		if err != nil || !bytes.Equal(buf, []byte(ModePortForwardServer)) {
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

	// initiate handshake
	log.Debugf("port forward server waiting for handshake")
	if err = cDealer.SendSignal(&PortForwardSignal{
		Version: protocolVersion,
		Type:    SignalHandShake,
	}); err != nil {
		log.Error("command channel handshake failed")
		return err
	}
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
	log.Debugf("port forward server handshake complete")

	// now start listening
	listener, err := net.Listen("tcp", config.ListenHost+":"+strconv.Itoa(int(config.ListenPort)))
	if err != nil {
		return err
	}
	defer listener.Close()

	// setup state machine
	// command read thread
	var commandReadC = make(chan *PortForwardSignal)
	t.Go(func() error {
		defer func() {
			log.Debugf("command channel dismantling")
			close(commandReadC)
			log.Debugf("command channel dismantled")
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
	// connection read thread
	var connReadC = make(chan net.Conn)
	t.Go(func() error {
		defer func() {
			log.Debugf("listener channel dismantling")
			close(connReadC)
			log.Debugf("listener channel dismantled")
		}()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if err == io.EOF {
					break
				} else if strings.Contains(err.Error(), "use of closed network connection") {
					log.Debugf("read from a closed socket")
					break
				} else if strings.Contains(err.Error(), "broken pipe") {
					break
				} else {
					log.Errorf("%s, %+v\n", "unexpected error", err)
					break
				}
			}
			connReadC <- conn
		}
		return nil
	})

	// main loop
	log.Infof("port forward server started")
	log.Infof("listen on %s:%d", config.ListenHost, config.ListenPort)
	config.PortForwardClientLoop(t, commonSession, commandReadC, connReadC, cDealer)
	// TODO: wait and handle exit

	return nil
}

func (config ForwardPortClientConfig) PortForwardClientLoop(
	t *tomb.Tomb,
	commonSession pairconnect.MuxSession,
	commandReadC chan *PortForwardSignal,
	connReadC chan net.Conn,
	cDealer signalDealer, // just for writing to peer
) error {
	var err error
	var state PortForwardClientState = ClientStateWait
	var pendingConn net.Conn = nil
	defer func() {
		if pendingConn != nil {
			pendingConn.Close()
		}
	}()
	for {
		// detect cancellation
		select {
		case <-t.Dying():
			return nil
		default:
		}
		switch state {
		case ClientStateWait: // wait for more connection, or server's goodbye
			select {
			case <-t.Dying():
				return nil
			case commandResult, ok := <-commandReadC:
				if !ok {
					// command socket closed
					log.Debugf("command channel closed")
					return nil
				}
				switch commandResult.Type {
				case SignalBye:
					log.Debugf("command channel bye")
					return nil
				default:
					log.Debugf("command channel abnormal message received")
					return nil
				}
			case connResult, ok := <-connReadC:
				if !ok {
					// command socket closed
					log.Debugf("listener channel closed")
					return nil
				}
				// ask server if connection is ok
				if err = cDealer.SendSignal(&PortForwardSignal{
					Version: protocolVersion,
					Type:    SignalConnect,
				}); err != nil {
					log.Error("listener channel signal failed")
					return err
				}
				pendingConn = connResult
				state = ClientStateConn1
			}
		case ClientStateConn1: // client has sent the request, waiting for SignalConnectAgree or SignalConnectRefuse
			select {
			case <-t.Dying():
				return nil
			case commandResult, ok := <-commandReadC:
				if !ok {
					// command socket closed
					log.Debugf("command channel closed")
					return nil
				}
				switch commandResult.Type {
				case SignalConnectAgree:
					log.Infof("peer agreed port forward request")
					_pendingConn := pendingConn
					t.Go(func() error {
						startPairing(t.Context(nil), commonSession, _pendingConn, false)
						return nil
					})
					state = ClientStateWait
					pendingConn = nil
				case SignalConnectRefuse:
					log.Warning("peer refused port forward request")
					state = ClientStateWait
					pendingConn.Close()
					pendingConn = nil
				case SignalBye:
					log.Debugf("command channel bye")
					return nil
				default:
					log.Debugf("command channel abnormal message received")
					return nil
				}
			}
		}
	}
}
