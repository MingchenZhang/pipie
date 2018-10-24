package internal

import (
	"errors"
	"github.com/xtaci/kcp-go"
	"net"
	"reflect"
	"sync"
	"time"
)

type encryptionSpec struct {
	key []byte
}

func BuildReliableUDPConn(udpConn net.PacketConn, remoteAddr net.Addr, asServer bool, eSpec *encryptionSpec) (net.Conn, error) {
	var udpSession *kcp.UDPSession
	var err error
	if asServer {
		var listener *kcp.Listener
		if eSpec != nil {
			encryptBlock, err := kcp.NewAESBlockCrypt(eSpec.key)
			if err != nil {
				log.Error(err)
				return nil, errors.New("cannot build reliable UDP Conn: cannot create encryption block")
			}
			listener, err = kcp.ServeConn(encryptBlock, 10, 3, udpConn)
		}else {
			listener, err = kcp.ServeConn(nil, 10, 3, udpConn)
		}
		if err != nil {
			log.Error(err)
			return nil, errors.New("cannot build reliable UDP Conn: kcp.ServeConn")
		}
		udpSession, err = listener.AcceptKCP()
		if err != nil {
			log.Error(err)
			return nil, errors.New("cannot build reliable UDP Conn: kcp.AcceptKCP")
		}
		configKCPSession(udpSession)
		var mutex = &sync.Mutex{}
		var closeCounter byte = 1
		var wrapper = &connWrapper{
			conn: udpSession,
			beforeClose: func() {
				// wait for all packet to be sent out
				k := reflect.ValueOf(*udpSession).FieldByName("kcp").Elem()
				snd_buf := k.FieldByName("snd_buf")
				snd_queue := k.FieldByName("snd_queue")
				var timestamp = time.Now()
				for snd_buf.Len() > 0 || snd_queue.Len() > 0 {
					if time.Now().Sub(timestamp) > time.Second * 2 {
						log.Warningf("waiting for send queue clearing took too long")
						//timestamp = time.Now() // wait instead of break
						break // break instead of wait
					}
					time.Sleep(time.Millisecond * 10)
				}
				//time.Sleep(time.Millisecond * 100)
			},
			afterClose: func() {
				mutex.Lock()
				if closeCounter > 0 {
					listener.Close()
					closeCounter--
				}
				mutex.Unlock()
			},
		}
		return wrapper, nil
	} else {
		if eSpec != nil {
			encryptBlock, err := kcp.NewAESBlockCrypt(eSpec.key)
			if err != nil {
				log.Error(err)
				return nil, errors.New("cannot build reliable UDP Conn: cannot create encryption block")
			}
			udpSession, err = kcp.NewConn(remoteAddr.String(), encryptBlock, 10, 3, udpConn)
		}else {
			udpSession, err = kcp.NewConn(remoteAddr.String(), nil, 10, 3, udpConn)
		}
		if err != nil {
			log.Error(err)
			return nil, errors.New("cannot build reliable UDP Conn: kcp.NewConn")
		}
		configKCPSession(udpSession)
		var wrapper = &connWrapper{
			conn: udpSession,
			beforeClose: func() {
				// wait for all packet to be sent out
				k := reflect.ValueOf(*udpSession).FieldByName("kcp").Elem()
				snd_buf := k.FieldByName("snd_buf")
				snd_queue := k.FieldByName("snd_queue")
				var timestamp = time.Now()
				for snd_buf.Len() > 0 || snd_queue.Len() > 0 {
					if time.Now().Sub(timestamp) > time.Second * 2 {
						log.Warningf("waiting for send queue clearing took too long")
						//timestamp = time.Now() // wait instead of break
						break // break instead of wait
					}
					time.Sleep(time.Millisecond * 10)
				}
				//time.Sleep(time.Millisecond * 100)
			},
		}
		return wrapper, nil
	}
}

func configKCPSession(session *kcp.UDPSession) {
	session.SetMtu(1300)
	session.SetWindowSize(1024, 1024)
	session.SetNoDelay(0, 10, 2, 1)
	session.SetStreamMode(true)
}


