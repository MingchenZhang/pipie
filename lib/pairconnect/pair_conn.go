package pairconnect

import (
	"bytes"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"github.com/hashicorp/yamux"
	"github.com/op/go-logging"
	"io/ioutil"
	"net"
	"net/url"
	"reflect"
	"time"
	"unsafe"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
	syncthingrelayclient "github.com/syncthing/syncthing/lib/relay/client"
	syncthingrelayproto "github.com/syncthing/syncthing/lib/relay/protocol"

	syncthinglogger "github.com/syncthing/syncthing/lib/logger"
)

const handshakeMagic = "magic_pipie"

type PairConnectionConfig struct {
	connBuilder buildConnectionConfig
	polarity    bool // false: client, true: server
}

var log, _ = logging.GetLogger("pairconnect")

type PairConnBuilder struct {
	config *PairConnectionConfig
}

func init() {
	// disable the syncthing nasty logging to stdout
	if syncthinglogger.DefaultLogger == nil {
		panic("syncthinglogger not initialized")
	}
	l := reflect.ValueOf(syncthinglogger.DefaultLogger).Elem().FieldByName("logger")
	if !l.IsValid() {
		panic("syncthinglogger logger not found")
	}
	le := reflect.NewAt(l.Type(), unsafe.Pointer(l.UnsafeAddr())).Elem()
	le.MethodByName("SetOutput").Call([]reflect.Value{reflect.ValueOf(ioutil.Discard)})
}

func NewPairConnConfigBuilder() *PairConnBuilder {
	builder := new(PairConnBuilder)
	builder.config = &PairConnectionConfig{
		connBuilder: buildConnectionConfig{
			serverAddress: "pipie.mingc.net:7890",
			udpConnection: true,
			relayPool:     nil,
			forceRelay:    false,
			peerDeviceID:  nil,
			cert:          nil,
		},
	}
	return builder
}

func (builder *PairConnBuilder) SetPairIDAndKey(pairID []byte, accessKey []byte) {
	builder.config.connBuilder.pairID = pairID
	builder.config.connBuilder.key = accessKey
}

func (builder *PairConnBuilder) SetRendezvousServer(server string) *PairConnBuilder {
	builder.config.connBuilder.serverAddress = server
	return builder
}

func (builder *PairConnBuilder) SetTLS(cert *tls.Certificate) *PairConnBuilder {
	builder.config.connBuilder.cert = cert
	return builder
}

func (builder *PairConnBuilder) SetRelayPerm(pool *url.URL) {
	builder.config.connBuilder.relayPool = pool
}

func (builder *PairConnBuilder) SetRelayTemp(pool *url.URL, peerDeviceID *syncthingprotocol.DeviceID) {
	builder.config.connBuilder.relayPool = pool
	builder.config.connBuilder.peerDeviceID = peerDeviceID
}

func (builder *PairConnBuilder) SetForceRelay() {
	builder.config.connBuilder.forceRelay = true
}

func (builder *PairConnBuilder) Build() (*PairConnectionConfig, error) {
	// TODO: sanity check
	builder.config.polarity = builder.config.connBuilder.peerDeviceID == nil
	if builder.config.connBuilder.cert == nil {
		return nil, errors.New("certificate not specified")
	}
	return builder.config, nil
}

func (config PairConnectionConfig) ConnectTo() (net.Conn, error) {
	conn, _, err := config.connectTo()
	return conn, err
}

func (config PairConnectionConfig) ConnectToWithMux() (MuxSession, error) {
	conn, _, err := config.connectTo()
	if err != nil {
		return nil, err
	}
	var session MuxSession
	//if config.polarity {
	//	session, err = smux.Client(conn, nil)
	//} else {
	//	session, err = smux.Server(conn, nil)
	//}
	if config.polarity {
		session, err = yamux.Client(conn, nil)
	} else {
		session, err = yamux.Server(conn, nil)
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (config PairConnectionConfig) connectTo() (net.Conn, *pairInfoMeta, error) {
	var peerConn net.Conn
	var connectionMeta *pairInfoMeta
	var err error
	var newConn net.Conn
	if config.connBuilder.forceRelay {
		log.Infof("direct connection skipped")
		goto RelaySkip
	}
	if config.connBuilder.udpConnection {
		var udpConn net.PacketConn
		var peerAddr net.Addr
		udpConn, peerAddr, connectionMeta, err = config.connBuilder.buildConnectionUDP()
		if err != nil {
			if config.connBuilder.relayPool != nil {
				goto RelaySkip
			}
			log.Error("unable to build udp connection to peer")
			return nil, nil, err
		}
		peerConn, err = stabilifyUDPConn(udpConn, peerAddr, connectionMeta.Polarity, nil)
		if err != nil {
			log.Error("unable to build reliable udp connection to peer")
			return nil, nil, err
		}
	} else {
		peerConn, connectionMeta, err = config.connBuilder.buildConnectionTCP()
		if err != nil {
			if config.connBuilder.relayPool != nil {
				goto RelaySkip
			}
			log.Error(err)
			return nil, nil, err
		}
	}
RelaySkip:
	// TODO: smarter relay selection algorithm and retry after failure
	if peerConn == nil {
		if config.connBuilder.relayPool == nil {
			log.Error("relay error: relay not configured")
			return nil, nil, errors.New("relay not configured")
		}
		// need to use relay
		log.Infof("connecting to a relay")
		if config.polarity {
			// syncthing relay permanent mode
			log.Debugf("choosing syncthing relay permanent mode")
			if logging.GetLevel("") == logging.DEBUG {
				log.Debugf("as DeviceID %s", syncthingprotocol.NewDeviceID(config.connBuilder.cert.Certificate[0]).String())
			}
			client, err := syncthingrelayclient.NewClient(
				config.connBuilder.relayPool,
				[]tls.Certificate{*config.connBuilder.cert},
				nil,
				time.Second*5)
			if err != nil {
				log.Error("relay error: failed to create client")
				return nil, nil, err
			}
			go client.Serve()
			if client.Error() != nil {
				log.Errorf("relay error: client cannot connect")
				client.Stop()
				return nil, nil, client.Error()
			}
			inv := syncthingrelayproto.SessionInvitation{}
			select {
			case inv = <-client.Invitations():
				break
			case <-time.After(time.Second * 10):
				log.Error("relay error: no invitation received")
				client.Stop()
				return nil, nil, errors.New("timeout")
			}
			client.Stop()
			log.Debugf("joining relay session")
			relayedConn, err := syncthingrelayclient.JoinSession(inv)
			if err != nil {
				log.Error("relay error: failed to join session")
				return nil, nil, err
			}
			peerConn = relayedConn
		} else {
			// syncthing relay temporary mode
			log.Debugf("choosing syncthing relay temporary mode")
			log.Debugf("to DeviceID %s", config.connBuilder.peerDeviceID.String())
			inv, err := syncthingrelayclient.GetInvitationFromRelay(
				config.connBuilder.relayPool,
				*config.connBuilder.peerDeviceID,
				[]tls.Certificate{*config.connBuilder.cert},
				time.Second*10)
			if err != nil {
				log.Error("relay error: failed to get invitation from relay")
				return nil, nil, err
			}
			log.Debugf("joining relay session")
			relayedConn, err := syncthingrelayclient.JoinSession(inv)
			if err != nil {
				log.Error("relay error: failed to join session")
				return nil, nil, err
			}
			peerConn = relayedConn
		}
	}
	log.Info("peer connected")
	/*
		dhParam, err := tls.DhParamsPEM([]byte(dhParam))
		errorAssert(err)
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{tls.TLS_DHE_PSK_WITH_AES_128_GCM_SHA256},
			//ServerName: "name",
			InsecureSkipVerify: true,
			Certificates: []tls.Certificate{},
			DhParameters: &dhParam,
			//Extra: psk.PSKConfig{
			//	GetKey: func(identity string) ([]byte, error) {
			//		return key, nil
			//	},
			//	GetIdentity: func() string {
			//		return "client"
			//	},
			//},
			GetPSKIdentityHint: func() ([]byte, error) {
				return []byte("client"), nil
			},
			GetPSKIdentity: func(identityHint []byte) (string, error) {
				return "client", nil
			},
			GetPSKKey: func(identity string) ([]byte, error) {
				return key, nil
			},
		}
		var peerTLSConn *tls.Conn
		if connectionMeta.Polarity {
			log.Debug("to connect as server")
			peerTLSConn = tls.Server(peerConn, tlsConfig)
		}else{
			log.Debug("to connect as client")
			peerTLSConn = tls.Client(peerConn, tlsConfig)
		}
		defer func() {if newConn != nil {newConn.Close()}}()
		if err := peerTLSConn.Handshake(); err != nil {
			log.Errorf("tls handshake failed, %+v", err)
			return nil, nil, err
		}
		newConn = peerTLSConn
	*/
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		CipherSuites:       []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{*config.connBuilder.cert},
		ClientAuth:         tls.RequireAnyClientCert,
	}
	var peerTLSConn *tls.Conn
	if (connectionMeta != nil && connectionMeta.Polarity) || (connectionMeta == nil && config.polarity) {
		log.Debug("to connect as server")
		peerTLSConn = tls.Server(peerConn, tlsConfig)
	} else {
		log.Debug("to connect as client")
		peerTLSConn = tls.Client(peerConn, tlsConfig)
	}
	defer func() {
		if newConn != nil {
			newConn.Close()
		}
	}()
	if err := peerTLSConn.Handshake(); err != nil {
		log.Errorf("tls handshake failed, %+v", err)
		return nil, nil, err
	}
	newConn = peerTLSConn

	// send the magic and the authentication
	passed, err := tlsIdentityCheck(config.connBuilder.cert, peerTLSConn, config.connBuilder.key)
	if !passed {
		return nil, nil, err
	}
	returnConn := newConn
	newConn = nil // so don't get deferred close
	return returnConn, connectionMeta, nil
}

func tlsIdentityCheck(myCert *tls.Certificate, conn *tls.Conn, key []byte) (bool, error) {
	var err error
	hash := sha512.New() // algorithm used for identity key
	hash.Write(myCert.Certificate[0])
	hash.Write(key)
	myIdentityKey := hash.Sum(nil)
	hash.Reset()
	hash.Write(conn.ConnectionState().PeerCertificates[0].Raw)
	hash.Write(key)
	peerIdentityKey := hash.Sum(nil)
	magic := []byte(handshakeMagic)
	_, err = conn.Write(magic)
	_, err = conn.Write(myIdentityKey)
	if err != nil {
		return false, err
	}
	{
		var buf [len(handshakeMagic)]byte
		var bufC int = 0
		for {
			n, err := conn.Read(buf[bufC:])
			if err != nil {
				return false, err
			}
			bufC += n
			if bufC == len(buf) {
				break
			}
		}
		if !bytes.Equal(buf[:], magic) {
			return false, errors.New("magic mismatch")
		}
		log.Debugf("magic checked")
	}
	{
		var buf = make([]byte, hash.Size())
		var bufC int = 0
		for {
			n, err := conn.Read(buf[bufC:])
			if err != nil {
				return false, err
			}
			bufC += n
			if bufC == len(buf) {
				break
			}
		}
		if !bytes.Equal(buf[:], peerIdentityKey) {
			log.Debugf("expecting %s, got %s", hex.EncodeToString(peerIdentityKey), hex.EncodeToString(buf))
			return false, errors.New("identity mismatch")
		}
		log.Debugf("identity checked")
	}
	return true, nil
}
