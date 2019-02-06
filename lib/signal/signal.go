package signal

import (
	"github.com/op/go-logging"
	"os"
	"os/signal"
	"syscall"
)

var log, _ = logging.GetLogger("signal")

var SignalSIGINT = make(chan os.Signal)
var SignalSIGPIPE = make(chan os.Signal)

var SigIgnore = make(map[os.Signal]bool)

func init() {
	RegisterSignalHandler()
}

func IgnoreSignalDefault(sig os.Signal) {
	log.Debugf("ignoreSignalDefault(%s)", sig)
	SigIgnore[sig] = true
}

func UnIgnoreSignalDefault(sig os.Signal) {
	log.Debugf("unIgnoreSignalDefault(%s)", sig)
	delete(SigIgnore, sig)
}

func RegisterSignalHandler() {
	var sigExitChan = make(chan os.Signal)
	signal.Notify(sigExitChan, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGPIPE)
	go func() {
		for {
			s := <-sigExitChan
			log.Debugf("signal received: %+v", s)
			switch s {
			case syscall.SIGINT:
				select {
				case SignalSIGINT <- s:
					break
				default:
					if _, ok := SigIgnore[s]; ok {
						log.Debugf("SIGINT default ignored")
					} else {
						log.Debugf("SIGINT default: exit")
						os.Exit(128 + int(syscall.SIGINT))
					}
				}
			case syscall.SIGPIPE:
				select {
				case SignalSIGPIPE <- s:
					break
				default:
					if _, ok := SigIgnore[s]; ok {
						log.Debugf("SIGPIPE default ignored")
					} else {
						log.Debugf("SIGPIPE default: exit")
						os.Exit(128 + int(syscall.SIGPIPE))
					}
				}
			default:
				log.Debugf("%s default: exit", s)
				os.Exit(128)
			}
		}
	}()
}
