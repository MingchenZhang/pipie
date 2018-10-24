package internal

import (
	"bytes"
	"errors"
	"github.com/raff/tls-ext"
	"github.com/raff/tls-psk"
	"github.com/xtaci/smux"
	"net"
)

const handshakeMagic = "magic_pipie"

type SecureSocketConfig struct{
	ConnBuilder BuildConnectionConfig
	UseTLS bool
}

func (config SecureSocketConfig) connectTo(pairID string, key []byte) (net.Conn, *PairInfoMeta, error) {
	var peerConn net.Conn
	var connectionMeta *PairInfoMeta
	var err error
	var newConn net.Conn
	if config.UseTLS {
		// use tls
		// TODO: update TLS version (which also has to support tls-psk), so that the close can unblock blocked write
		if config.ConnBuilder.UDPConnection {
			var udpConn net.PacketConn
			var peerAddr net.Addr
			udpConn, peerAddr, connectionMeta, err = config.ConnBuilder.BuildConnectionUDP(pairID)
			if err != nil {
				log.Error("unable to build udp connection to peer")
				return nil, nil, err
			}
			peerConn, err = BuildReliableUDPConn(udpConn, peerAddr, connectionMeta.Polarity, nil)
			if err != nil {
				log.Error("unable to build reliable udp connection to peer")
				return nil, nil, err
			}
		}else {
			peerConn, connectionMeta, err = config.ConnBuilder.BuildConnectionTCP(pairID)
			if err != nil {
				log.Error(err)
				return nil, nil, err
			}
		}
		log.Info("peer connected")
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{psk.TLS_PSK_WITH_AES_128_CBC_SHA},
			Certificates: []tls.Certificate{tls.Certificate{}},
			Extra: psk.PSKConfig{
				GetKey: func(identity string) ([]byte, error) {
					return key, nil
				},
				GetIdentity: func() string {
					return "client"
				},
			},
		}
		var peerTLSConn *tls.Conn
		if connectionMeta.Polarity {
			log.Debug("connect as server")
			peerTLSConn = tls.Server(peerConn, tlsConfig)
		}else{
			log.Debug("connect as client")
			peerTLSConn = tls.Client(peerConn, tlsConfig)
		}
		defer func() {if newConn != nil {newConn.Close()}}()
		if err := peerTLSConn.Handshake(); err != nil {
			log.Error("tls handshake failed")
			return nil, nil, err
		}
		newConn = peerTLSConn
	} else {
		// not use tls
		if config.ConnBuilder.UDPConnection {
			var udpConn net.PacketConn
			var peerAddr net.Addr
			udpConn, peerAddr, connectionMeta, err = config.ConnBuilder.BuildConnectionUDP(pairID)
			if err != nil {
				log.Error("unable to build udp connection to peer")
				return nil, nil, err
			}
			eSpec := new(encryptionSpec)
			eSpec.key = key
			peerConn, err = BuildReliableUDPConn(udpConn, peerAddr, connectionMeta.Polarity, eSpec)
			if err != nil {
				log.Error("unable to build reliable udp connection to peer")
				return nil, nil, err
			}
		} else {
			log.Warningf("not using TLS on TCP connection leaves connection unencrypted")
			peerConn, connectionMeta, err = config.ConnBuilder.BuildConnectionTCP(pairID)
			if err != nil {
				log.Error(err)
				return nil, nil, err
			}
		}
		log.Info("peer connected")
		newConn = peerConn
	}

	// send the magic
	//var newConn = peerConn
	magic := []byte(handshakeMagic)
	_, err = newConn.Write(magic) // TODO: looping needed?
	if err != nil {
		return nil, nil, err
	}
	var buf [len(handshakeMagic)]byte
	var bufC int = 0
	for {
		n, err := newConn.Read(buf[bufC:])
		if err != nil {
			return nil, nil, err
		}
		bufC += n
		if bufC == len(buf) {
			break
		}
	}
	if !bytes.Equal(buf[:], magic) {
		return nil, nil, errors.New("magic mismatch")
	}
	log.Debugf("magic checked")
	returnConn := newConn
	newConn = nil // so don't get deferred close
	return returnConn, connectionMeta, nil
}

func (config SecureSocketConfig) ConnectTo(pairID string, key []byte) (net.Conn, error) {
	conn, _, err := config.connectTo(pairID, key)
	return conn, err
}

func (config SecureSocketConfig) ConnectToWithMux(pairID string, key []byte) (muxSession, error) {
	conn, meta, err := config.connectTo(pairID, key)
	if err != nil {
		return nil, err
	}
	var session muxSession
	if meta.Polarity {
		session, err = smux.Client(conn, nil)
	} else {
		session, err = smux.Server(conn, nil)
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}
