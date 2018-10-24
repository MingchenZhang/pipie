package internal

import (
	"os"
	"os/signal"
	"syscall"
)

var signalSIGINT = make(chan os.Signal)
var signalSIGPIPE = make(chan os.Signal)

var sigIgnore = make(map[os.Signal]bool)

func init() {
	registerSignalHandler()
}

func ignoreSignalDefault(sig os.Signal) {
	log.Debugf("ignoreSignalDefault(%s)", sig)
	sigIgnore[sig] = true
}

func unIgnoreSignalDefault(sig os.Signal) {
	log.Debugf("unIgnoreSignalDefault(%s)", sig)
	delete(sigIgnore, sig)
}

func registerSignalHandler() {
	var sigExitChan = make(chan os.Signal)
	signal.Notify(sigExitChan, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGPIPE)
	go func() {
		for {
			s := <-sigExitChan
			log.Debugf("signal received: %+v", s)
			switch s {
			case syscall.SIGINT:
				select{
				case signalSIGINT <- s:
					break
				default:
					if _, ok := sigIgnore[s]; ok {
						log.Debugf("SIGINT default ignored")
					}else{
						log.Debugf("SIGINT default: exit")
						os.Exit(128+int(syscall.SIGINT))
					}
				}
			case syscall.SIGPIPE:
				select{
				case signalSIGPIPE <- s:
					break
				default:
					if _, ok := sigIgnore[s]; ok {
						log.Debugf("SIGPIPE default ignored")
					}else{
						log.Debugf("SIGPIPE default: exit")
						os.Exit(128+int(syscall.SIGPIPE))
					}
				}
			default:
				log.Debugf("%s default: exit", s)
				os.Exit(128)
			}
		}
	}()
}
