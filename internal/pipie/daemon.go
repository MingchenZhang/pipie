package pipie

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/golang/protobuf/proto"
	"github.com/mingchenzhang/pipie/internal/pipie/exchangestruct"
	"github.com/mingchenzhang/pipie/internal/pipie/permission"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
	"github.com/op/go-logging"
	"gopkg.in/restruct.v1"
	"gopkg.in/tomb.v2"
	"net"
	"runtime/debug"
	"strings"
	"time"
)

const (
	DaemonModePipeInlet = iota
	DaemonModePipeOutlet
	DaemonModePortForwardClient
	DaemonModePortForwardServer
)

type daemon struct {
	transferCode         [32]byte             // username of the node
	signalEncryptionHash [32]byte             // bytes used in signal payload encryption, generated from username
	conn                 net.Conn             // connection to signaling server
	accessKey            []byte               // master key for authentication from client
	tokenMap             map[string]time.Time // token that is generated by this node // TODO: need a routine to clean it up regularly
	permission           *permission.Checker
	daemonServiceConfig  *DaemonServiceConfig
}

func DaemonLoop(
	ctx context.Context,
	serviceConfig *DaemonServiceConfig,
	signalServerURL string,
	username string,
	accessKey string,
	pmc *permission.Checker,
) error {
	var t *tomb.Tomb
	t, ctx = tomb.WithContext(ctx)
	defer func() {
		t.Kill(nil)
		t.Wait()
	}()
	daemon := &daemon{
		tokenMap:            make(map[string]time.Time),
		daemonServiceConfig: serviceConfig,
	}
	hash := sha256.New()
	copy(daemon.transferCode[:], generateTransferCode(username))
	copy(daemon.signalEncryptionHash[:], generateSignalEncryptionKey(username))
	//signalServerAddress, err := net.ResolveUDPAddr("udp", signalServerURL)
	//if err != nil {
	//	log.Errorf("couldn't resolve signal server url: $s", signalServerURL)
	//	return err
	//}
	dialer := new(net.Dialer)
	conn, err := dialer.DialContext(ctx, "udp", signalServerURL)
	if err != nil {
		log.Errorf("couldn't create connection to signal server url: $s", conn.RemoteAddr().String())
		return err
	}
	go func() {
		<-t.Dying()
		conn.Close()
	}()
	defer conn.Close()
	daemon.conn = conn
	daemon.accessKey = []byte(accessKey)
	hash.Reset()
	hash.Write(daemon.daemonServiceConfig.Cert.Certificate[0])
	copy(daemon.daemonServiceConfig.CertHash, hash.Sum(nil))
	daemon.permission = pmc

	// start pinging signal server
	t.Go(func() error {
		for {
			pingPacket := constructTransferPacker(daemon.transferCode[:], make([]byte, 32), []byte(nil))
			_, err = conn.Write(pingPacket)
			if err != nil && !strings.Contains(err.Error(), "connection refused") {
				log.Error(err)
				return nil
			}
			select {
			case <-t.Dying():
				return nil
			case <-time.After(10 * time.Second):
				continue
			}
		}
	})
	// start listening
	var receiveBuf [1500]byte
	for {
		n, err := conn.Read(receiveBuf[:])
		if err != nil {
			if strings.Contains(err.Error(), "connection refused") {
				continue
			} else {
				log.Error(err)
				break
			}
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error(r)
					if log.IsEnabledFor(logging.DEBUG) {
						log.Debug(string(debug.Stack()))
					}
				}
			}()
			if err := daemon.handleExchange(ctx, t, receiveBuf[:n]); err != nil {
				log.Error(err)
			}
		}()

	}
	return nil
}

func (d *daemon) handleExchange(ctx context.Context, t *tomb.Tomb, oriPayload []byte) error {
	// peal off the transfer layer
	if oriPayload[0] != transferVersion {
		return errors.New("transfer version mismatch")
	}
	if len(oriPayload) < TransferFrameSize {
		return errors.New("payload too small")
	}
	transferFrame := TransferFrame{}
	err := restruct.Unpack(oriPayload[:TransferFrameSize], binary.BigEndian, &transferFrame)
	if err != nil {
		log.Errorf("unable to unpack transfer packet")
		return err
	}
	if !bytes.Equal(transferFrame.Destination[:], d.transferCode[:]) {
		log.Errorf("transfer packet destination not matched")
		return err
	}
	payload := oriPayload[TransferFrameSize:]
	exchange := exchangestruct.ExchangeFrame{}
	err = proto.Unmarshal(payload, &exchange)
	if err != nil {
		log.Errorf("unable to unmarshal payload")
		return err
	}
	switch exchange.ExchangeBody.(type) {
	case *exchangestruct.ExchangeFrame_ChallengeRequest:
		{
			// TODO: rate limit this
			token := getRandomBytes(ChallengeTokenSize)
			d.tokenMap[hex.EncodeToString(token)] = time.Now()
			// sending exchange reply
			exchangeRet := exchangestruct.ExchangeFrame{
				ExchangeBody: &exchangestruct.ExchangeFrame_Challenge{
					Challenge: &exchangestruct.ExchangeFrame_ExchangeChallenge{
						Token:   token,
						Payload: []byte{},
					},
				},
			}
			sendPayload, err := proto.Marshal(&exchangeRet)
			if err != nil {
				log.Errorf("unable to marshal payload")
				return err
			}
			wholeFrame := constructTransferPacker(d.transferCode[:], transferFrame.Source[:], sendPayload)
			if len(wholeFrame) > 1300 {
				log.Warningf("sending frame larger then 1300 bytes")
			}
			d.conn.Write(wholeFrame)
		}
	case *exchangestruct.ExchangeFrame_Challenge:
		return errors.New("exchange type error (is ExchangeFrame_Challenge)")
	case *exchangestruct.ExchangeFrame_ChallengeResponse:
		{
			response := exchange.GetExchangeBody().(*exchangestruct.ExchangeFrame_ChallengeResponse).ChallengeResponse
			if _, ok := d.tokenMap[hex.EncodeToString(response.Token)]; !ok {
				log.Warningf("unseen or expired token is received")
				return errors.New("token not found")
			}
			plainPayload, err := exchangeDecrypt(generateEncryptionNonce(response.Token, response.Nonce), d.signalEncryptionHash[:], response.Payload)
			if err != nil {
				return errors.New("cannot decrypt and authenticate challenge")
			}
			responsePayload := exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload{}
			err = proto.Unmarshal(plainPayload, &responsePayload)
			if err != nil {
				log.Errorf("unable to marshal payload")
				return err
			}
			// prepare some fields
			pairKey := responsePayload.PairKey
			relayURL, err := getRelayServer()
			if err != nil {
				log.Error(err)
				return errors.New("unable to get relay server")
			}
			pairID := hex.EncodeToString(getRandomBytes(PairIDLength))
			// switch intention type
			switch responsePayload.Intention.(type) {
			case *exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PipeOut:
				{
					_ = responsePayload.Intention.(*exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PipeOut).PipeOut
					if !d.permission.CheckPipeOut() {
						err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
							Result:  proto.Bool(false),
							Message: proto.String("failed permission check"),
						})
						if err != nil {
							return err
						}
						break
					}
					err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
						Result:    proto.Bool(true),
						Message:   nil,
						Signature: d.daemonServiceConfig.CertHash,
						Relay:     proto.String(relayURL.String()),
						PairID:    proto.String(pairID),
					})
					if err != nil {
						return err
					}
					// construct connection specific configuration
					var pairConnBuilder = pairconnect.NewPairConnConfigBuilder()
					pairConnBuilder.SetPairIDAndKey([]byte(pairID), generatePairConnKey(pairKey, d.accessKey))
					pairConnBuilder.SetRendezvousServer(NatRendezvousServer)
					pairConnBuilder.SetTLS(d.daemonServiceConfig.Cert)
					if d.daemonServiceConfig.ForceRelay {
						pairConnBuilder.SetForceRelay()
					}
					pairConnBuilder.SetRelayPerm(relayURL)
					pairConnConfig, err := pairConnBuilder.Build()
					if err != nil {
						return err
					}
					err = pipeOut(ctx, pairConnConfig)
					log.Debugf("pipe session stopped")
					if err != nil {
						return err
					}
					// stop the daemon after the pipe is stopped
					return nil
				}
			case *exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardTo:
				{
					detail := responsePayload.Intention.(*exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardTo).PortForwardTo
					if !d.permission.CheckPortForwardIn(*detail.DestIP, int(*detail.DestPort)) {
						err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
							Result:  proto.Bool(false),
							Message: proto.String("failed permission check"),
						})
						if err != nil {
							return err
						}
						break
					}
					err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
						Result:    proto.Bool(true),
						Message:   nil,
						Signature: d.daemonServiceConfig.CertHash,
						Relay:     proto.String(relayURL.String()),
						PairID:    proto.String(pairID),
					})
					if err != nil {
						return err
					}
					// construct connection specific configuration
					var pairConnBuilder = pairconnect.NewPairConnConfigBuilder()
					pairConnBuilder.SetPairIDAndKey([]byte(pairID), generatePairConnKey(pairKey, d.accessKey))
					pairConnBuilder.SetRendezvousServer(NatRendezvousServer)
					pairConnBuilder.SetTLS(d.daemonServiceConfig.Cert)
					if d.daemonServiceConfig.ForceRelay {
						pairConnBuilder.SetForceRelay()
					}
					pairConnBuilder.SetRelayPerm(relayURL)
					pairConnConfig, err := pairConnBuilder.Build()
					if err != nil {
						return err
					}
					t.Go(func() error {
						defer func() {
							if err := recover(); err != nil {
								log.Error(err)
							}
						}()
						portForwardFrom(ctx, pairConnConfig, *detail.DestIP, uint16(*detail.DestPort))
						log.Debugf("forward session stopped")
						return nil
					})
				}
			case *exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardFrom:
				{
					detail := responsePayload.Intention.(*exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardFrom).PortForwardFrom
					if !d.permission.CheckPortForwardOut(*detail.BindIP, int(*detail.BindPort)) {
						err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
							Result:  proto.Bool(false),
							Message: proto.String("failed permission check"),
						})
						if err != nil {
							return err
						}
						break
					}
					err = d.daemonChallengeConfirm(response.Token, transferFrame.Source[:], &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{
						Result:    proto.Bool(true),
						Message:   nil,
						Signature: d.daemonServiceConfig.CertHash,
						Relay:     proto.String(relayURL.String()),
						PairID:    proto.String(pairID),
					})
					if err != nil {
						return err
					}
					// construct connection specific configuration
					var pairConnBuilder = pairconnect.NewPairConnConfigBuilder()
					pairConnBuilder.SetPairIDAndKey([]byte(pairID), generatePairConnKey(pairKey, d.accessKey))
					pairConnBuilder.SetRendezvousServer(NatRendezvousServer)
					pairConnBuilder.SetTLS(d.daemonServiceConfig.Cert)
					if d.daemonServiceConfig.ForceRelay {
						pairConnBuilder.SetForceRelay()
					}
					pairConnBuilder.SetRelayPerm(relayURL)
					pairConnConfig, err := pairConnBuilder.Build()
					if err != nil {
						return err
					}
					t.Go(func() error {
						defer func() {
							if err := recover(); err != nil {
								log.Error(err)
							}
						}()
						portForwardTo(ctx, pairConnConfig, *detail.BindIP, uint16(*detail.BindPort))
						log.Debugf("forward session stopped")
						return nil
					})
				}
			default:
				return errors.New("intention type unknown")
			}
			// delete token
			delete(d.tokenMap, hex.EncodeToString(response.Token))
		}
	case *exchangestruct.ExchangeFrame_Confirm:
		return errors.New("exchange type error (is ExchangeFrame_Confirm)")
	default:
		return errors.New("exchange type unknown")
	}
	return nil
}

func generateEncryptionNonce(token []byte, nonce []byte) []byte {
	hash := sha256.New() // algorithm used for identity key
	hash.Write(token)
	hash.Write(nonce)
	return hash.Sum(nil)[:12]
}

func getRandomBytes(size int) []byte {
	b := make([]byte, size)
	n, err := rand.Read(b)
	if n < size || err != nil {
		panic(err)
	}
	return b
}

func exchangeDecrypt(nonce []byte, key []byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aesgcm.Open(nil, nonce, ciphertext, nil)
}

func exchangeEncrypt(nonce []byte, key []byte, plaintext []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aesgcm.Seal(nil, nonce, plaintext, nil)
}

func constructTransferPacker(source []byte, dest []byte, payload []byte) []byte {
	frame := make([]byte, TransferFrameSize+len(payload))
	frame[0] = transferVersion
	copy(frame[1:33], source)
	copy(frame[33:65], dest)
	copy(frame[65:], payload)
	return frame
}

func constructEncryptedPayload(nonce []byte, key []byte, pb proto.Message) []byte {
	plainPayload, err := proto.Marshal(pb)
	if err != nil {
		log.Errorf("unable to marshal payload")
		panic(err)
	}
	result := exchangeEncrypt(nonce, key, plainPayload)
	return result
}

func (d *daemon) daemonChallengeConfirm(token []byte, dest []byte, payload *exchangestruct.ExchangeFrame_ExchangeConfirm_Payload) error {
	nonce := getRandomBytes(PayloadNonceSize)
	exchangeRet := exchangestruct.ExchangeFrame{
		ExchangeBody: &exchangestruct.ExchangeFrame_Confirm{
			Confirm: &exchangestruct.ExchangeFrame_ExchangeConfirm{
				Token: token,
				Nonce: nonce,
				Payload: constructEncryptedPayload(
					generateEncryptionNonce(token, nonce),
					d.signalEncryptionHash[:],
					payload),
			},
		},
	}
	sendPayload, err := proto.Marshal(&exchangeRet)
	if err != nil {
		log.Errorf("unable to marshal payload")
		return err
	}
	wholeFrame := constructTransferPacker(d.transferCode[:], dest, sendPayload)
	if len(wholeFrame) > 1300 {
		log.Warningf("sending frame larger then 1300 bytes")
	}
	d.conn.Write(wholeFrame)
	return nil
}

//func run(allowMode map[int]bool, asServer bool, runOnce bool) (error) {
//	var packetConn, err = net.ListenPacket("udp", "pipie.mingc.net")
//	if err != nil {
//		return err
//	}
//	var serverAddr, err = net.ResolveUDPAddr("udp", "pipie.mingc.net")
//	if err != nil {
//		return err
//	}
//	var packetChan, _ = packetchan.CreateChan(packetConn, 512, serverAddr)
//
//}
//
//func (config *Daemon) sendCommand(packetChan packetchan.PacketChan, command *DaemonCommand) (error) {
//	if config.UserCode == nil {
//		config.generateUserCode()
//	}
//	// make exchange request
//	tsfFrame := TransferFrame{
//		transferVersion,
//		nil,
//		ExchangeFrame{
//			exchangeVersion,
//			ExchangeTypeChallengeRequest,
//			nil,
//		},
//	}
//	copy(tsfFrame.UserCode[:], config.UserCode)
//	data, err := restruct.Pack(binary.BigEndian, &tsfFrame)
//	errorAssert(err)
//	var requestRemain = 30
//SendRequest:
//	if requestRemain <= 0 {
//		return errors.New("timeout")
//	}
//	packetChan.WriteChan() <- packetchan.BufNAddr{
//		Buf:  data,
//		Addr: config.ServerAddr,
//	}
//	requestRemain--
//
//	// read challenge
//	var response packetchan.BufNAddr
//	var ok bool
//	select {
//	case response, ok = <-packetChan.ReadChan():
//		if !ok {
//			return errors.New("can't read challenge")
//		}
//		break
//	case <-time.After(time.Millisecond * 500):
//		goto SendRequest
//	}
//
//	restruct.Unpack(response.Buf, binary.BigEndian, &tsfFrame) // ignore error, error checking is done later
//	if transferVersion != tsfFrame.TransferVersion || exchangeVersion != tsfFrame.ExchangeFrame.ExchangeVersion {
//		return errors.New("version mismatch")
//	}
//	if tsfFrame.ExchangeFrame.ExchangeType != ExchangeTypeChallenge {
//		return errors.New("unexpected reply received, expecting ExchangeTypeChallenge")
//	}
//	exchangeRequest := ExchangeChallenge{}
//	restruct.Unpack(tsfFrame.ExchangeFrame.Exchange[:], binary.BigEndian, &exchangeRequest)
//	var challengeToken = exchangeRequest.Token
//
//	// generate challenge response
//}
//
//func recvCommand(packetChan packetchan.PacketChan) {
//}
//
//func (config *Daemon) generateUserCode() {
//	var buf = config.Username
//	for i := 0; i < UserCodeIteration; i++ {
//		h := sha256.New()
//		h.Write(buf)
//		buf = h.Sum(nil)
//	}
//	config.UserCode = buf
//}
