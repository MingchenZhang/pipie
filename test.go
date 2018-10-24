package main

import (
	"context"
	"flag"
	"github.com/op/go-logging"
	"os"
	pipie "github.com/mingchenzhang/pipie/internal"
	"strconv"
)

var log, _ = logging.GetLogger("pipie")

func main() {
	logBackend2 := logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend2)
	logBackendLeveled.SetLevel(logging.DEBUG, "")
	var format = logging.MustStringFormatter(
		`%{color}%{shortfunc}:%{shortfile} ▶ %{level:.4s}%{color:reset} %{message}`,
	)
	backendFormatter := logging.NewBackendFormatter(logBackend2, format)
	logBackendLeveled.SetLevel(logging.DEBUG, "")
	logging.SetBackend(backendFormatter)

	pairID := flag.String("pairID", "", "id for paring")
	key := flag.String("key", "this is a secret", "key for encryption")
	exchangeServerIP := flag.String("serverIP", "pipie.mingc.net", "serverIP")
	exchangeServerPort := flag.Int("serverPort", 7890, "serverPort")
	asServer := flag.Bool("asServer", false, "asServer")
	forwardToIP := flag.String("forwardToIP", "localhost", "where to forward to")
	forwardToPort := flag.Int("forwardToPort", 1234, "where to forward to")
	listenIP := flag.String("listenIP", "localhost", "where to listen")
	listenPort := flag.Int("listenPort", 1234, "where to listen")
	asPipe := flag.Bool("asPipe", false, "asPipe")
	useTCP := flag.Bool("useTCP", false, "useTCP")
	useTLS := flag.Bool("useTLS", false, "useTLS")

	flag.Parse()

	if *pairID == "" {
		log.Critical("pairID must be provided")
		os.Exit(1)
	}


	var secureConfig = pipie.SecureSocketConfig{
		ConnBuilder: pipie.BuildConnectionConfig{
			ServerAddress: *exchangeServerIP + ":" + strconv.Itoa(*exchangeServerPort),
			UDPConnection: !*useTCP,
		},
		UseTLS: *useTLS,
	}
	var commandSession, err = secureConfig.ConnectToWithMux(*pairID, []byte(*key))
	if err != nil {
		panic(err)
	}
	defer commandSession.Close()
	log.Infof("command channel created")


	if *asPipe {
		if *asServer {
			err = pipie.PipeOutlet(context.TODO(), commandSession)
			if err != nil {panic(err)}
		} else {
			err = pipie.PipeInlet(context.TODO(), commandSession)
			if err != nil {panic(err)}
		}
	} else {

		if *asServer {
			// setup the server
			var config = pipie.ForwardPortServerConfig{
				TargetPort: *forwardToPort,
				TargetIP:   *forwardToIP,
				SockConfig: secureConfig,
			}
			err = config.ForwardPortServer(context.TODO(), commandSession)
			if err != nil {panic(err)}
		} else {
			// setup the client
			var config = pipie.ForwardPortClientConfig{
				SockConfig: secureConfig,
				ListenIP: *listenIP,
				ListenPort: *listenPort,
				TargetIP: "",
				TargetPort: 0,
			}
			err = config.ForwardPortClient(context.TODO(), commandSession)
			if err != nil {panic(err)}
		}
	}
}
