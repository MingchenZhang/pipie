package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/mingchenzhang/pipie/internal/pipie"
	"github.com/mingchenzhang/pipie/internal/pipie/permission"
	"github.com/mingchenzhang/pipie/lib/signal"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
	"gopkg.in/tomb.v2"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/akamensky/argparse"
)

func main() {
	TestMain()
}

var log, _ = logging.GetLogger("pipie")

// TODO: check all error handling
// TODO: check all timeout (freeze)
// TODO: handle panic in daemon
// TODO: test port forward, dest is not reachable (and other failures)
// TODO: investigate port forward server mem leakage if client crash. unclosed kcp conn or what not
func TestMain() {
	var err error

	logBackend := logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend)

	debugPrint := os.Getenv("DEBUG_PRINT") != ""
	if debugPrint {
		var format = logging.MustStringFormatter(
			`%{color}%{shortfunc}:%{shortfile} ▶ %{level:.4s}%{color:reset} %{message}`,
		)
		backendFormatter := logging.NewBackendFormatter(logBackend, format)
		logBackendLeveled.SetLevel(logging.DEBUG, "")
		logging.SetBackend(backendFormatter)
	} else {
		var format = logging.MustStringFormatter(
			`%{level:.4s} %{message}`,
		)
		backendFormatter := logging.NewBackendFormatter(logBackend, format)
		logBackendLeveled.SetLevel(logging.INFO, "")
		logging.SetBackend(backendFormatter)
	}

	parser := argparse.NewParser("pipie", "Connect machines across network")
	argMode := parser.String("m", "mode", &argparse.Options{
		Required: true,
		Help:     "Mode to run. " + pipie.ModePipe + " or " + pipie.ModePortForward + ".",
		Validate: func(args []string) error {
			if len(args) == 0 {
				return errors.New("no mode specified")
			}
			switch args[0] {
			case pipie.ModePipe, pipie.ModePortForward:
				break
			default:
				return errors.New(fmt.Sprintf("mode %s cannot be recognized", args[0]))
			}
			return nil
		},
	})
	argReverseForward := parser.Flag("r", "reverse-forward", &argparse.Options{
		Required: false,
		Help:     "Run in reversed port forward mode. If not set, program will run in normal port forward mode. Only valid if mode is " + pipie.ModePortForward,
	})
	argForwardAddresses := parser.String("a", "forward-address", &argparse.Options{
		Required: false,
		Help:     "Format: bindHost:bindPort:destHost:destPort",
		Validate: func(args []string) error {
			if len(args) == 0 {
				return errors.New("no forward addresses specified")
			}
			parts := strings.Split(args[0], ":")
			if len(parts) != 4 {
				return errors.New("no enough forward addresses given")
			}
			// TODO: check more format
			return nil
		},
	})
	argAsServer := parser.Flag("s", "server", &argparse.Options{
		Required: false,
		Help:     "Run as a client. If not used, program will run as server. See --help for further detail.",
	})
	argForceRelay := parser.Flag("", "use-relay", &argparse.Options{
		Required: false,
		Help:     "Force connection to use relay. Server and client has to use same setting",
	})
	argUserName := parser.String("n", "name", &argparse.Options{
		Required: true,
		Help:     "Username used. Specify a username for connection.",
		Validate: func(args []string) error {
			if len(args) == 0 {
				return errors.New("no username specified")
			}
			if len(args[0]) < 6 {
				return errors.New("username is too short")
			}
			return nil
		},
	})
	// TODO: read access key from file
	argAccessKey := parser.String("k", "access-key", &argparse.Options{
		Required: false,
		Help:     "Access key used. Specify a access key for connection. WARNING: Sensitive data in arguments is usually not secure",
		Validate: func(args []string) error {
			if len(args) == 0 {
				return errors.New("no accesskey specified")
			}
			return nil
		},
	})
	argSignalServer := parser.String("", "signal-server", &argparse.Options{
		Required: false,
		Help:     "Specify a signal server to use. Default value is " + pipie.SignalServer,
		Default:  pipie.SignalServer,
	})
	argRendezvousServer := parser.String("r", "rendezvous-server", &argparse.Options{
		Required: false,
		Help:     "Specify a rendezvous server to use. Default value is " + pipie.NatRendezvousServer,
		Default:  pipie.NatRendezvousServer,
	})

	err = parser.Parse(os.Args)
	if err != nil {
		log.Fatal(err)
	}

	var bindHost, destHost string = "", ""
	var bindPort, destPort uint16 = 0, 0
	if *argMode == pipie.ModePortForward && !*argAsServer {
		forwardAddresses := strings.Split(*argForwardAddresses, ":")
		bindHost = forwardAddresses[0]
		bindPort64, err := strconv.ParseInt(forwardAddresses[1], 10, 32)
		if err != nil {
			log.Critical(err)
		}
		bindPort = uint16(bindPort64)
		destHost = forwardAddresses[2]
		destPort64, err := strconv.ParseInt(forwardAddresses[3], 10, 32)
		if err != nil {
			log.Critical(err)
		}
		destPort = uint16(destPort64)
	}

	// TODO: if filesystem is readonly, generate and store cert in memory
	var certFile, keyFile string
	var cert tls.Certificate
	dir := "."
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Debugf("certificate not exist, generating certificate and key file")
		generateCert(certFile, keyFile)
		log.Debugf("cert generated")
	}
	cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("cannot load certificate: %s", err)
	}

	hash := sha256.New()
	hash.Write(cert.Certificate[0])
	certHash := hash.Sum(nil)
	serviceConfig := &pipie.DaemonServiceConfig{
		RendezvousServer: *argRendezvousServer,
		Cert:             &cert,
		CertHash:         certHash,
		ForceRelay:       *argForceRelay,
	}

	// create context for closure
	t, ctx := tomb.WithContext(context.Background())
	go func() {
		// handle signal goroutine
		select {
		case <-signal.SignalSIGINT:
			t.Kill(nil)
		case <-t.Dead():
		}
	}()
	// ignore not needed SIGPIPE signal
	signal.IgnoreSignalDefault(syscall.SIGPIPE)
	defer signal.UnIgnoreSignalDefault(syscall.SIGPIPE)

	if *argAsServer {
		// TODO: generate proper permission
		pmRegister := permission.NewRegister()
		pmRegister.AllowPipeOut()
		if !*argReverseForward {
			pmRegister.AllowPortForwardIn("", 0)
		} else {
			pmRegister.AllowPortForwardOut("", 0)
		}
		err = pipie.DaemonLoop(ctx, serviceConfig, *argSignalServer, *argUserName, *argAccessKey, pmRegister.BuildChecker())
		if err != nil {
			log.Error(err)
		}
	} else {
		switch *argMode {
		case pipie.ModePipe:
			err = pipie.PipeClient(ctx, serviceConfig, *argSignalServer, *argUserName, *argAccessKey)
		case pipie.ModePortForward:
			if *argReverseForward {
				err = pipie.PortForwardFromClient(
					ctx,
					serviceConfig,
					*argSignalServer,
					*argUserName,
					*argAccessKey,
					bindHost,
					uint32(bindPort),
					destHost,
					uint32(destPort))
			} else {
				err = pipie.PortForwardToClient(
					ctx,
					serviceConfig,
					*argSignalServer,
					*argUserName,
					*argAccessKey,
					bindHost,
					uint32(bindPort),
					destHost,
					uint32(destPort))
			}
		default:
			log.Fatalf("Mode not supported")
		}
		if err != nil {
			log.Error(err)
		}
	}
}

func generateCert(certName string, keyName string) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"plain_pair"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // TODO: increase duration and detect cert expiration
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	certOut, err := os.Create(certName)
	if err != nil {
		log.Fatalf("failed to open cert.pem for writing: %s", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		log.Fatalf("failed to write data to cert.pem: %s", err)
	}
	if err := certOut.Close(); err != nil {
		log.Fatalf("error closing cert.pem: %s", err)
	}
	keyOut, err := os.OpenFile(keyName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("failed to open key.pem for writing:", err)
		return
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		log.Fatalf("failed to write data to key.pem: %s", err)
	}
	if err := keyOut.Close(); err != nil {
		log.Fatalf("error closing key.pem: %s", err)
	}
}
