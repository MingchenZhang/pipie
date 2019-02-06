package pipie

import "github.com/op/go-logging"

var log, _ = logging.GetLogger("pipie")

const (
	ChallengeTokenSize = 32
	PairIDLength       = 32
	PayloadNonceSize   = 12
	PairingKeySize     = 32
)

const (
	transferVersion uint8 = 1
)

const (
	ModePipe        = "pipe"
	ModePortForward = "port_forward"
)

const (
	SignalServer        = "pipie.mingc.net:4567"
	NatRendezvousServer = "pipie.mingc.net:7890"
)
