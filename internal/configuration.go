package internal

import "github.com/op/go-logging"

var log, _ = logging.GetLogger("pipie")

const (
	ModeStringLen         = 14
	ModePortForwardServer = "MODE_PORT_SERV"
	ModePortForwardClient = "MODE_PORT_CLIE"
	ModePipeInlet         = "MODE_PIPE_INLE"
	ModePipeOutlet        = "MODE_PIPE_OUTL"
)

const (
	TraversalAttempRetry = 4
	TraversalTimeout = 8
)

var (
	protocolVersion int = 1
)
