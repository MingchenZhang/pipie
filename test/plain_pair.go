package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
	"github.com/op/go-logging"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	pipieio "github.com/mingchenzhang/pipie/lib/io"

	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
)

var log, _ = logging.GetLogger("pipie")

func main() {
	logBackend := logging.NewLogBackend(os.Stderr, "", 0)
	logBackendLeveled := logging.AddModuleLevel(logBackend)
	logBackendLeveled.SetLevel(logging.DEBUG, "")
	var format = logging.MustStringFormatter(
		`%{color}%{shortfunc}:%{shortfile} ▶ %{level:.4s}%{color:reset} %{message}`,
	)
	backendFormatter := logging.NewBackendFormatter(logBackend, format)
	logBackendLeveled.SetLevel(logging.DEBUG, "")
	logging.SetBackend(backendFormatter)

	pairID := flag.String("pairID", "", "id for paring")
	key := flag.String("key", "this is a secret", "key for encryption")
	exchangeServerIP := flag.String("serverIP", "pipie.mingc.net", "serverIP")
	exchangeServerPort := flag.Int("serverPort", 7890, "serverPort")
	asServer := flag.Bool("asServer", false, "asServer")
	forwardToHost := flag.String("forwardToHost", "localhost", "where to forward to")
	forwardToPort := flag.Int("forwardToPort", 1234, "where to forward to")
	listenIP := flag.String("listenIP", "localhost", "where to listen")
	listenPort := flag.Int("listenPort", 1234, "where to listen")
	asPipe := flag.Bool("asPipe", false, "asPipe")
	//useTCP := flag.Bool("useTCP", false, "useTCP")
	useTLS := flag.Bool("useTLS", true, "useTLS")
	peerDeviceID := flag.String("peerDeviceID", "", "peerDeviceID")

	flag.Parse()

	if *pairID == "" {
		log.Critical("pairID must be provided")
		os.Exit(1)
	}

	// generate certificate if not exist
	var certFile, keyFile string
	var cert tls.Certificate
	dir := "."
	if *asServer {
		certFile, keyFile = filepath.Join(dir, "server_cert.pem"), filepath.Join(dir, "server_key.pem")
	} else {
		certFile, keyFile = filepath.Join(dir, "client_cert.pem"), filepath.Join(dir, "client_key.pem")
	}
	var err error
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Debugf("certificate not exist, generating certificate and key file")
		generateCert(certFile, keyFile)
		log.Debugf("cert generated")
	}
	cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("cannot load certificate: %s", err)
	}

	myCertID := syncthingprotocol.NewDeviceID(cert.Certificate[0]).String()
	log.Infof("Device ID: %s", myCertID)

	var secureConfigBuilder = pairconnect.NewPairConnConfigBuilder()
	secureConfigBuilder.SetRendezvousServer(*exchangeServerIP + ":" + strconv.Itoa(*exchangeServerPort))
	if *useTLS {
		secureConfigBuilder.SetTLS(&cert)
	}
	// relay testing
	//secureConfigBuilder.SetForceRelay()
	relayURI, err := url.Parse("relay://31.220.45.3:22067/?id=OHWS367-33NI2O3-GRQQVWG-6SHK655-ETRIJCC-GYTLNCT-2CBUXX5-XFFHCQ4&pingInterval=1m0s&networkTimeout=2m0s&sessionLimitBps=0&globalLimitBps=0&statusAddr=:22070&providedBy=https://blog.veloc1ty.de")
	if err != nil {
		log.Critical(err)
	}
	if *asServer {
		secureConfigBuilder.SetRelayPerm(relayURI)
	} else {
		if *peerDeviceID == "" {
			log.Fatalf("peerDeviceID not given")
		}
		peerID, err := syncthingprotocol.DeviceIDFromString(*peerDeviceID)
		if err != nil {
			log.Fatalf("DeviceIDFromString: %s", err)
		}
		secureConfigBuilder.SetRelayTemp(relayURI, &peerID)
	}

	secureConfig, err := secureConfigBuilder.Build()
	if err != nil {
		panic(err)
	}
	commandSession, err := secureConfig.ConnectToWithMux(*pairID, []byte(*key))
	if err != nil {
		panic(err)
	}
	defer commandSession.Close()
	log.Infof("command channel created")

	if *asPipe {
		if *asServer {
			err = pipieio.PipeOutlet(context.TODO(), commandSession)
			if err != nil {
				panic(err)
			}
		} else {
			err = pipieio.PipeInlet(context.TODO(), commandSession)
			if err != nil {
				panic(err)
			}
		}
	} else {
		if *asServer {
			// setup the server
			var config = pipieio.ForwardPortServerConfig{
				DestPort: uint16(*forwardToPort),
				DestHost: *forwardToHost,
			}
			err = config.ForwardPortServer(context.TODO(), commandSession)
			if err != nil {
				panic(err)
			}
		} else {
			// setup the client
			var config = pipieio.ForwardPortClientConfig{
				ListenHost: *listenIP,
				ListenPort: uint16(*listenPort),
			}
			err = config.ForwardPortClient(context.TODO(), commandSession)
			if err != nil {
				panic(err)
			}
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
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // TODO: increase duration
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
