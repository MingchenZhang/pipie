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
	"github.com/mingchenzhang/pipie/internal/pipie"
	"github.com/op/go-logging"
	"math/big"
	"os"
	"path/filepath"
	"time"
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

	var certFile, keyFile string
	var cert tls.Certificate
	dir := "."
	certFile, keyFile = filepath.Join(dir, "client_cert.pem"), filepath.Join(dir, "client_key.pem")
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

	hash := sha256.New()
	hash.Write(cert.Certificate[0])
	certHash := hash.Sum(nil)
	serviceConfig := &pipie.DaemonServiceConfig{
		RendezvousServer: "pipie.mingc.net:7890",
		Cert:             &cert,
		CertHash:         certHash,
		ForceRelay:       false,
	}
	err = pipie.PipeClient(context.Background(), serviceConfig, "pipie.mingc.net:4567", "test_server", "abc")
	if err != nil {
		panic(err)
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
