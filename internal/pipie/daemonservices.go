package pipie

import (
	"context"
	"crypto/tls"
	pipieio "github.com/mingchenzhang/pipie/lib/io"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
)

// static configuration used for pair connection
type DaemonServiceConfig struct {
	RendezvousServer string
	Cert             *tls.Certificate
	CertHash         []byte // 32 bytes
	ForceRelay       bool   // TODO: make this force relay a per connection setting
}

// if peerDeviceID is not nil, it imply this peer is the temporary side of syncthing relay connection
func pipeIn(ctx context.Context, connectionConfig *pairconnect.PairConnectionConfig) error {
	session, err := connectionConfig.ConnectToWithMux()
	if err != nil {
		log.Error(err)
		return err
	}
	defer session.Close()
	log.Infof("peer connected")
	err = pipieio.PipeInlet(ctx, session)
	if err != nil {
		return err
	}
	return nil
}

// if peerDeviceID is not nil, it imply this peer is the temporary side of syncthing relay connection
func pipeOut(ctx context.Context, connectionConfig *pairconnect.PairConnectionConfig) error {
	session, err := connectionConfig.ConnectToWithMux()
	if err != nil {
		log.Error(err)
		return err
	}
	defer session.Close()
	log.Infof("peer connected")
	err = pipieio.PipeOutlet(ctx, session)
	if err != nil {
		return err
	}
	return nil
}

// this usually runs in separated thread
// if peerDeviceID is not nil, it imply this peer is the temporary side of syncthing relay connection
// it connects to host and port if there is an incoming connection
func portForwardFrom(ctx context.Context, connectionConfig *pairconnect.PairConnectionConfig, host string, port uint16) {
	session, err := connectionConfig.ConnectToWithMux()
	if err != nil {
		log.Error(err)
		return
	}
	defer session.Close()
	log.Infof("peer connected")
	var c = pipieio.ForwardPortServerConfig{
		DestPort: port,
		DestHost: host,
	}
	err = c.ForwardPortServer(ctx, session)
	if err != nil {
		log.Error(err)
	}
}

// this usually runs in separated thread
// if peerDeviceID is not nil, it imply this peer is the temporary side of syncthing relay connection
// host and port indicate the host and port of the listening socket
func portForwardTo(ctx context.Context, connectionConfig *pairconnect.PairConnectionConfig, host string, port uint16) {
	session, err := connectionConfig.ConnectToWithMux()
	if err != nil {
		log.Error(err)
		return
	}
	defer session.Close()
	log.Infof("peer connected")
	var c = pipieio.ForwardPortClientConfig{
		ListenHost: host,
		ListenPort: port,
	}
	err = c.ForwardPortClient(ctx, session)
	if err != nil {
		log.Error(err)
	}
}
