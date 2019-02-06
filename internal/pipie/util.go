package pipie

import (
	"crypto/rand"
	"crypto/sha256"
	"net/url"
)

func getRelayServer() (*url.URL, error) {
	return url.Parse("relay://31.220.45.3:22067/?id=OHWS367-33NI2O3-GRQQVWG-6SHK655-ETRIJCC-GYTLNCT-2CBUXX5-XFFHCQ4&pingInterval=1m0s&networkTimeout=2m0s&sessionLimitBps=0&globalLimitBps=0&statusAddr=:22070&providedBy=https://blog.veloc1ty.de")
}

func generatePairConnKey(pairKey []byte, accessKey []byte) []byte {
	hash := sha256.New() // right size for AES256
	hash.Write(pairKey)
	hash.Write([]byte(accessKey))
	return hash.Sum(nil)
}

func generateTransferCode(username string) []byte {
	hash := sha256.New()
	hash.Write([]byte(username))
	return hash.Sum(nil)
}

func generateTransferCodeRandom() []byte {
	hash := sha256.New()
	transferCode := make([]byte, hash.Size())
	if _, err := rand.Read(transferCode); err != nil {
		panic(err)
	}
	return transferCode
}

func generateSignalEncryptionKey(username string) []byte {
	hash := sha256.New()
	hash.Write([]byte(username))
	hash.Write([]byte("username"))
	return hash.Sum(nil)
}
