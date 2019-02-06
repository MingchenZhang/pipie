package pairconnect

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

type pairInfos struct {
	Status    bool     `json:"status"`
	Code      int      `json:"code,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	PairInfo  pairInfo `json:"pairInfo"`  // self
	Pair2Info pairInfo `json:"pair2Info"` // peer
}

type pairInfo struct {
	PublicIP      string       `json:"publicIP"`
	PublicPort    string       `json:"publicPort"`
	InterfaceIP   string       `json:"interfaceIP"`
	InterfacePort string       `json:"interfacePort"`
	Meta          pairInfoMeta `json:"meta"`
}

type pairInfoMeta struct {
	Polarity bool `json:"polarity"`
}

type buildConnectionConfig struct {
	pairID        []byte
	key           []byte
	serverAddress string
	udpConnection bool
	relayPool     *url.URL // must be a static relay address
	forceRelay    bool
	peerDeviceID  *syncthingprotocol.DeviceID // nil if permanent mode
	cert          *tls.Certificate
}

func setReusableFD(network, address string, c syscall.RawConn) error {
	errChan := make(chan error)
	c.Control(func(fd uintptr) {
		err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if err != nil {
			log.Notice("socket set SO_REUSEADDR failed")
			errChan <- err
			return
		}
		// SO_REUSEPORT
		err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 0xf, 1)
		if err != nil {
			log.Notice("socket set SO_REUSEPORT failed")
			errChan <- err
			return
		}
		errChan <- nil
	})
	return <-errChan
}

func getReuseableDialer() net.Dialer {
	dialer := net.Dialer{
		Control: setReusableFD,
	}
	return dialer
}

func getReuseableListenConfig() net.ListenConfig {
	config := net.ListenConfig{
		Control: setReusableFD,
	}
	return config
}

func connect(doneC chan byte, result chan net.Conn, localAddr net.Addr, peerAddr string) {
	dialer := getReuseableDialer()
	dialer.LocalAddr = localAddr
	dialer.Timeout, _ = time.ParseDuration("1000ms")
	for {
		select {
		case <-doneC:
			return
		default:
		}
		// TODO: may leak connection if other succeed
		conn, err := dialer.Dial("tcp", peerAddr)
		if err == nil {
			result <- conn
			<-doneC
			return
		}
		log.Debug("connecting")
		//if conn != nil {conn.Close()}
		nerr := err.(net.Error)
		if !nerr.Timeout() {
			log.Error("connect: encounter non timeout issue")
			log.Error(err)
			//result <- nil
			//return
		}
	}
}

func accept(doneC chan byte, result chan net.Conn, localAddr string) {
	config := getReuseableListenConfig()
	// TODO: what to do with context?
	listener, err := config.Listen(context.TODO(), "tcp", localAddr)
	if err != nil {
		log.Error("accept: cannot listen")
		log.Error(err)
		//result <- nil
		return
	}
	tcplistener := listener.(*net.TCPListener)
	for {
		select {
		case <-doneC:
			return
		default:
		}
		tcplistener.SetDeadline(time.Now().Add(time.Second * 1))
		// TODO: may leak connection if other succeed
		conn, err := tcplistener.Accept()
		if err == nil {
			result <- conn
			<-doneC
			return
		}
		log.Debug("accepting")
		//if conn != nil {conn.Close()}
		nerr := err.(net.Error)
		if !nerr.Timeout() {
			log.Error("accept: encounter non timeout issue")
			log.Error(err)
			//result <- nil
			return
		}
	}

}

// outdated comparing to udp
func (config buildConnectionConfig) buildConnectionTCP() (net.Conn, *pairInfoMeta, error) {
	// get a so_reuseaddr dialer
	dialer := getReuseableDialer()
	conn, err := dialer.Dial("tcp", config.serverAddress)
	if err != nil {
		log.Notice("failed to connect to traversal server")
		return nil, nil, err
	}
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	localAddr := conn.LocalAddr()
	localAddrA := strings.Split(localAddr.String(), ":")
	toSend := map[string]string{
		"pairID":        string(config.pairID), // safe as bytes are generated from string
		"interfaceIP":   localAddrA[0],
		"interfacePort": localAddrA[1],
	}
	toSendB, _ := json.Marshal(toSend)
	conn.Write(append(toSendB[:], []byte("\n")[:]...)) // TODO: should handle error?
	message, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		log.Notice("failed to read from traversal server")
		return nil, nil, err
	}
	//fmt.Println(message)
	pairInfo := new(pairInfos)
	if err := json.Unmarshal([]byte(message), &pairInfo); err != nil {
		log.Notice("read invalid message from traversal server")
		return nil, nil, err
	}
	log.Debugf("%+v\n", pairInfo)
	if !pairInfo.Status {
		log.Notice("traversal server status false")
		return nil, nil, err
	}
	pair1 := pairInfo.PairInfo
	pair2 := pairInfo.Pair2Info
	conn.Close()
	conn = nil

	doneC := make(chan byte)
	result := make(chan net.Conn)
	var peerConn net.Conn
	var totalGo int = 0
	if pair1.InterfacePort == pair1.PublicPort {
		go accept(doneC, result, pair1.InterfaceIP+":"+pair1.InterfacePort)
		totalGo += 1
	} else {
		go accept(doneC, result, pair1.InterfaceIP+":"+pair1.InterfacePort)
		go accept(doneC, result, pair1.InterfaceIP+":"+pair1.PublicPort)
		totalGo += 2
	}
	if pair2.InterfaceIP == pair2.PublicIP && pair2.InterfacePort == pair2.PublicPort {
		go connect(doneC, result, localAddr, pair2.InterfaceIP+":"+pair2.InterfacePort)
		totalGo += 1
	} else {
		go connect(doneC, result, localAddr, pair2.InterfaceIP+":"+pair2.InterfacePort)
		go connect(doneC, result, localAddr, pair2.PublicIP+":"+pair2.PublicPort)
		totalGo += 2
	}

	peerConn = <-result
	log.Info("buildConnectionTCP: peer connection established")
	for i := 0; i < totalGo; i++ {
		doneC <- 0
	}
	return peerConn, &pair1.Meta, nil
}

func (config buildConnectionConfig) buildConnectionUDP() (net.PacketConn, net.Addr, *pairInfoMeta, error) {
	ctx := context.Background()
	ctx, cancelFunc := context.WithCancel(ctx)

	// handle signal
	cancelSignalChan := make(chan os.Signal)
	signal.Notify(cancelSignalChan, os.Interrupt)
	signal.Notify(cancelSignalChan, os.Kill)
	defer signal.Reset(nil)
	go (func() error {
		// handle signal goroutine
		select {
		case <-cancelSignalChan:
			cancelFunc()
		case <-ctx.Done():
		}
		return nil
	})()

	var err error
	var firstMeta *pairInfoMeta
	for i := 0; i < traversalAttemptRetry; i++ {
		select {
		case <-ctx.Done():
			// cancelled
			if err == nil {
				err = errors.New("cancelled or timeout")
			}
			goto RETURN
		default:
		}
		log.Infof("beginning traversal round %d", i)
		ctx, _ := context.WithDeadline(ctx, time.Now().Add(time.Second*traversalTimeout))
		conn, addr, meta, erro := config.udpTraverse(ctx, i)
		if meta != nil && firstMeta == nil {
			firstMeta = meta
		}
		if erro != nil {
			err = erro
			time.Sleep(time.Second * 1)
			continue
		}
		return conn, addr, meta, nil
	}
RETURN:
	return nil, nil, nil, err
}

func (config buildConnectionConfig) udpTraverse(ctx context.Context, roundNum int) (net.PacketConn, net.Addr, *pairInfoMeta, error) {
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	var traversalDone = false

	var readBuf [1024]byte
	serverAddr, err := net.ResolveUDPAddr("udp", config.serverAddress)
	errorAssert(err)
	tempConn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		log.Error("failed to connect to traversal server")
		return nil, nil, nil, err
	}
	localAddr := tempConn.LocalAddr()
	tempConn.Close()
	conn, err := net.ListenPacket("udp", localAddr.String())
	if err != nil {
		log.Error("failed to connect to traversal server")
		return nil, nil, nil, err
	}
	log.Infof("listening on %s", localAddr.String())
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	//conn2, err := net.ListenPacket("udp", portPlusOne(localAddr.String()))
	//if err != nil {
	//	log.Debug(err)
	//} else {
	//	defer func() {
	//		if conn2 != nil {
	//			conn2.Close()
	//		}
	//	}()
	//	log.Debugf("listening on %s", conn2.LocalAddr().String())
	//}
	go func() {
		<-ctx.Done() // close if timeout or function returns
		if conn != nil {
			conn.Close()
		}
		//if conn2 != nil {
		//	conn2.Close()
		//}
	}()
	localAddrA := strings.Split(localAddr.String(), ":")
	toSend := map[string]string{
		"pairID":        string(config.pairID),
		"interfaceIP":   localAddrA[0],
		"interfacePort": localAddrA[1],
	}
	toSendB, _ := json.Marshal(toSend)
	_, err = conn.WriteTo(toSendB[:], serverAddr)
	errorAssert(err)
	// inform server once the process is interrupted (timeout or signal)
	defer func() {
		if !traversalDone {
			tmp, err := net.ListenPacket("udp", "")
			if err != nil {
				log.Debug(err)
				return
			}
			defer tmp.Close()
			_, err = tmp.WriteTo([]byte(`{"pairID":"`+string(config.pairID)+`","cancel":true}`), serverAddr)
			if err != nil {
				log.Debug(err)
				return
			}
		}
	}()
	var pairInfo *pairInfos
	var state uint32 = 0 // 0: waiting pairInfo. 1:sending ping, waiting ping or pong. 2:sending pong, waiting pong. 3:got pong
	var lastAddr net.Addr
	var lastAddr2 net.Addr
	// function to send out the noise to try to punch a hole
	var sendPingPong = func() {
		log.Debugf("start sending ping/pong")
		var delaySend = (roundNum%2 == 1) != (pairInfo != nil && pairInfo.PairInfo.Meta.Polarity)
		if delaySend {
			log.Debugf("delay ping/pong send set")
			time.Sleep(time.Millisecond * 1000)
		}
		for i := 0; i < 8; i++ {
			fromPeer := map[string]string{
				"fromPeer": "",
				"pairID":   string(config.pairID),
			}
			switch atomic.LoadUint32(&state) {
			case 1:
				fromPeer["fromPeer"] = "ping"
				f, _ := json.Marshal(fromPeer)
				conn.WriteTo(f, lastAddr)
				log.Debugf("sending %+v, to %+v", fromPeer, lastAddr)
				//if conn2 != nil {
				//	to := portPlusOneUDPAddr(lastAddr)
				//	conn2.WriteTo(f, to)
				//	log.Debugf("sending %+v, to %+v", fromPeer, to)
				//}
				if lastAddr2 != nil {
					conn.WriteTo(f, lastAddr2)
					log.Debugf("sending %+v, to %+v", fromPeer, lastAddr2)
					//if conn2 != nil {
					//	to := portPlusOneUDPAddr(lastAddr2)
					//	conn2.WriteTo(f, to)
					//	log.Debugf("sending %+v, to %+v", fromPeer, to)
					//}
				}
			case 2:
				fromPeer["fromPeer"] = "pong"
				f, _ := json.Marshal(fromPeer)
				conn.WriteTo(f, lastAddr)
				log.Debugf("sending %+v, to %+v", fromPeer, lastAddr)
				//if conn2 != nil {
				//	to := portPlusOneUDPAddr(lastAddr)
				//	conn2.WriteTo(f, to)
				//	log.Debugf("sending %+v, to %+v", fromPeer, to)
				//}
			default:
				return
			}
			time.Sleep(time.Millisecond * 500)
		}
	}
	// go into reading mode, expecting messages type: pairInfo, noise, ping from peer, or error message from server
	// both end: wait for pairInfo; keep sending ping. once receive ping, update address, keep sending pong on new address; once receive pong, update address and return
	for {
		var (
			n    int
			addr net.Addr
			err  error
		)
		//var readFrom = 0
		for {
			// read loop
			//readFrom = 0
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
			n, addr, err = conn.ReadFrom(readBuf[:])
			if err, ok := err.(net.Error); !ok || !err.Timeout() {
				break
			}
			//if conn2 == nil {
			//	continue
			//}
			//readFrom = 1
			//conn2.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
			//n, addr, err = conn2.ReadFrom(readBuf[:])
			//if err, ok := err.(net.Error); !ok || !err.Timeout() {
			//	break
			//}
		}
		if err != nil {
			log.Error("failed to read from UDP socket")
			if pairInfo != nil {
				return nil, nil, &pairInfo.PairInfo.Meta, err
			} else {
				return nil, nil, nil, err
			}
		}
		log.Debugf("receives: %s, from %s", string(readBuf[:n]), addr)
		var message map[string]interface{}
		if err := json.Unmarshal(readBuf[:n], &message); err != nil {
			log.Error(err)
			log.Error(string(readBuf[:n]))
			return nil, nil, nil, errors.New("read invalid message")
		}
		if val, ok := message["status"]; ok && val == false {
			// TODO: timeout handle
			return nil, nil, nil, errors.New("server returns false status")
		}
		if val, ok := message["fromPeer"]; ok {
			if atomic.LoadUint32(&state) == 0 {
				// do nothing
			} else if val.(string) == "ping" {
				atomic.StoreUint32(&state, 2)
				lastAddr = addr
				//if readFrom == 1 {
				//	conn.Close()
				//	conn = conn2
				//	conn2 = nil
				//} else if conn2 != nil {
				//	conn2.Close()
				//	conn2 = nil
				//}
			} else if val.(string) == "pong" {
				atomic.StoreUint32(&state, 3)
				lastAddr = addr
				//if readFrom == 1 {
				//	conn.Close()
				//	conn = conn2
				//	conn2 = nil
				//} else if conn2 != nil {
				//	conn2.Close()
				//	conn2 = nil
				//}
				// send one last pong
				fromPeer := map[string]string{
					"fromPeer": "",
					"pairID":   string(config.pairID),
				}
				fromPeer["fromPeer"] = "pong"
				f, _ := json.Marshal(fromPeer)
				conn.WriteTo(f, lastAddr)
				log.Debugf("sending %+v, to %+v", fromPeer, lastAddr)
				break
			}
		} else {
			pairInfo = new(pairInfos)
			if err := json.Unmarshal(readBuf[:n], pairInfo); err != nil {
				fmt.Println(err)
				return nil, nil, nil, errors.New("read invalid message")
			}
			pair2 := pairInfo.Pair2Info
			//log.Debugf("%+v\n", pairInfo)
			if !pairInfo.Status {
				return nil, nil, &pairInfo.PairInfo.Meta, errors.New("traversal server status false")
			}

			// update lastAddr
			lastAddr, err = net.ResolveUDPAddr("udp", pair2.PublicIP+":"+pair2.PublicPort)
			errorAssert(err)
			if pair2.PublicIP != pair2.InterfaceIP || pair2.PublicPort != pair2.InterfacePort {
				lastAddr2, err = net.ResolveUDPAddr("udp", pair2.InterfaceIP+":"+pair2.InterfacePort)
				errorAssert(err)
			} else {
				lastAddr2 = nil
			}
			atomic.StoreUint32(&state, 1)
			go sendPingPong()
		}
	}
	newLocalAddr := conn.LocalAddr()
	conn.Close()
	time.Sleep(time.Millisecond * 300)
	newConn, err := net.ListenPacket("udp", newLocalAddr.String())
	errorAssert(err)
	traversalDone = true
	return newConn, lastAddr, &pairInfo.PairInfo.Meta, nil
}
