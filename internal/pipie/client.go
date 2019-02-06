package pipie

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/golang/protobuf/proto"
	"github.com/mingchenzhang/pipie/internal/pipie/exchangestruct"
	"github.com/mingchenzhang/pipie/lib/pairconnect"
	"github.com/pkg/errors"
	syncthingprotocol "github.com/syncthing/syncthing/lib/protocol"
	"gopkg.in/restruct.v1"
	"net"
	"net/url"
)

func PipeClient(
	ctx context.Context,
	serviceConfig *DaemonServiceConfig,
	signalServerURL string,
	username string,
	accessKey string,
) error {
	payload := &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload{
		Intention: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PipeOut{
			PipeOut: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PipeOutDetail{},
		},
	}
	return client(
		ctx,
		serviceConfig,
		signalServerURL,
		username,
		accessKey,
		payload,
		func(pairBuilder *pairconnect.PairConnBuilder) error {
			pairConnConfig, err := pairBuilder.Build()
			if err != nil {
				return err
			}
			err = pipeIn(ctx, pairConnConfig)
			if err != nil {
				return err
			}
			return nil
		},
	)
}

// called "to" client, but send connection to server
func PortForwardToClient(
	ctx context.Context,
	serviceConfig *DaemonServiceConfig,
	signalServerURL string,
	username string,
	accessKey string,
	bindHost string,
	bindPort uint32,
	destHost string,
	destPort uint32,
) error {
	payload := &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload{
		Intention: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardTo{
			PortForwardTo: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardToDetail{
				DestIP:   proto.String(destHost),
				DestPort: proto.Uint32(destPort),
			},
		},
	}
	return client(
		ctx,
		serviceConfig,
		signalServerURL,
		username,
		accessKey,
		payload,
		func(pairBuilder *pairconnect.PairConnBuilder) error {
			pairConnConfig, err := pairBuilder.Build()
			if err != nil {
				return err
			}
			portForwardTo(ctx, pairConnConfig, bindHost, uint16(bindPort))
			return nil
		},
	)
}

// called "to" client, but send connection to server
func PortForwardFromClient(
	ctx context.Context,
	serviceConfig *DaemonServiceConfig,
	signalServerURL string,
	username string,
	accessKey string,
	bindHost string,
	bindPort uint32,
	destHost string,
	destPort uint32,
) error {
	payload := &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload{
		Intention: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardFrom{
			PortForwardFrom: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload_PortForwardFromDetail{
				BindIP:   proto.String(bindHost),
				BindPort: proto.Uint32(bindPort),
			},
		},
	}
	return client(
		ctx,
		serviceConfig,
		signalServerURL,
		username,
		accessKey,
		payload,
		func(pairBuilder *pairconnect.PairConnBuilder) error {
			pairConnConfig, err := pairBuilder.Build()
			if err != nil {
				return err
			}
			portForwardFrom(ctx, pairConnConfig, destHost, uint16(destPort))
			return nil
		},
	)
}

func client(
	ctx context.Context,
	serviceConfig *DaemonServiceConfig,
	signalServerURL string,
	username string,
	accessKey string,
	payload *exchangestruct.ExchangeFrame_ExchangeChallengeResponse_Payload,
	doPair func(pairBuilder *pairconnect.PairConnBuilder) error,
) error {
	var err error
	peerTransferCode := generateTransferCode(username)
	myTransferCode := generateTransferCodeRandom()
	signalEncryptionHash := generateSignalEncryptionKey(username)

	//signalAddr, err := net.ResolveUDPAddr("udp", signalServerURL)
	//if err != nil {
	//	log.Errorf("signal server address cannot be resolved")
	//	return err
	//}
	dialer := new(net.Dialer)
	conn, err := dialer.DialContext(ctx, "udp", signalServerURL)
	if err != nil {
		log.Errorf("couldn't create connection to signal server url: $s", conn.RemoteAddr().String())
		return err
	}

	// send challenge request
	{
		exchangeRet := exchangestruct.ExchangeFrame{
			ExchangeBody: &exchangestruct.ExchangeFrame_ChallengeRequest{
				ChallengeRequest: &exchangestruct.ExchangeFrame_ExchangeChallengeRequest{},
			},
		}
		sendPayload, err := proto.Marshal(&exchangeRet)
		if err != nil {
			return err
		}
		wholeFrame := constructTransferPacker(myTransferCode, peerTransferCode, sendPayload)
		if len(wholeFrame) > 1300 {
			log.Warningf("sending frame larger then 1300 bytes")
		}
		conn.Write(wholeFrame)
	}
	log.Debugf("challenge request sent")

	// data structure for receive
	receiveBuf := make([]byte, 1500)
	transferFrame := TransferFrame{}
	exchange := exchangestruct.ExchangeFrame{}
	// receive challenge
	{
		_, err = conn.Read(receiveBuf)
		if err != nil {
			return err
		}
		// decode
		err = restruct.Unpack(receiveBuf[:TransferFrameSize], binary.BigEndian, &transferFrame)
		if err != nil {
			return err
		}
		err = proto.Unmarshal(receiveBuf[TransferFrameSize:], &exchange)
	}

	challenge, ok := exchange.ExchangeBody.(*exchangestruct.ExchangeFrame_Challenge)
	if !ok {
		err := errors.New("receive unexpected response from peer")
		log.Errorf(err.Error())
		return err
	}
	token := challenge.Challenge.Token
	nonce := getRandomBytes(PayloadNonceSize)
	pairKey := getRandomBytes(PairingKeySize)
	log.Debugf("challenge received")

	// respond
	{
		// argument payload should have other details
		payload.PairKey = pairKey
		exchangeRet := exchangestruct.ExchangeFrame{
			ExchangeBody: &exchangestruct.ExchangeFrame_ChallengeResponse{
				ChallengeResponse: &exchangestruct.ExchangeFrame_ExchangeChallengeResponse{
					Token: token,
					Nonce: nonce,
					Payload: constructEncryptedPayload(
						generateEncryptionNonce(token, nonce),
						signalEncryptionHash,
						payload),
				},
			},
		}
		sendPayload, err := proto.Marshal(&exchangeRet)
		if err != nil {
			return err
		}
		wholeFrame := constructTransferPacker(myTransferCode, peerTransferCode, sendPayload)
		if len(wholeFrame) > 1300 {
			log.Warningf("sending frame larger then 1300 bytes")
		}
		conn.Write(wholeFrame)
	}
	log.Debugf("challenge response sent")

	// receive confirmation
	{
		_, err = conn.Read(receiveBuf)
		if err != nil {
			return err
		}
		// decode
		err = restruct.Unpack(receiveBuf[:TransferFrameSize], binary.BigEndian, &transferFrame)
		if err != nil {
			return err
		}
		err = proto.Unmarshal(receiveBuf[TransferFrameSize:], &exchange)
	}
	log.Debugf("challenge confirmation received")

	// check confirmation, get payload
	serverConfirmation := &exchangestruct.ExchangeFrame_ExchangeConfirm_Payload{}
	{
		confirmation, ok := exchange.ExchangeBody.(*exchangestruct.ExchangeFrame_Confirm)
		if !ok {
			err := errors.New("receive unexpected response from peer")
			log.Errorf(err.Error())
			return err
		}
		if !bytes.Equal(confirmation.Confirm.Token, token) {
			err := errors.New("receive unexpected token from peer")
			log.Errorf(err.Error())
			return err
		}
		// decrypt confirmation payload
		plainPayload, err := exchangeDecrypt(
			generateEncryptionNonce(confirmation.Confirm.Token, confirmation.Confirm.Nonce),
			signalEncryptionHash,
			confirmation.Confirm.Payload)
		if err != nil {
			return errors.New("cannot decrypt and authenticate confirmation")
		}
		err = proto.Unmarshal(plainPayload, serverConfirmation)
		if err != nil {
			return errors.New("cannot parse payload")
		}
	}
	log.Debugf("challenge confirmed")

	if !*serverConfirmation.Result {
		if serverConfirmation.Message == nil {
			err = errors.New("receive failing result")
		} else {
			err = errors.New("receive failing result: " + *serverConfirmation.Message)
		}
		log.Errorf(err.Error())
		return err
	}
	var relayURL *url.URL
	if serverConfirmation.Relay != nil {
		u, err := url.Parse(*serverConfirmation.Relay)
		if err == nil {
			relayURL = u
		}
	}
	if serverConfirmation.PairID == nil || pairKey == nil || relayURL == nil {
		err := errors.New("receive incomplete confirmation")
		log.Errorf(err.Error())
		return err
	}
	// TODO: check if serverConfirmation.Signature is known
	log.Debugf("all signaling checked, start pairing")
	conn.Close()
	peerDeviceID := syncthingprotocol.DeviceIDFromBytes(serverConfirmation.Signature)
	// construct connection specific configuration
	var pairConnBuilder = pairconnect.NewPairConnConfigBuilder()
	pairConnBuilder.SetPairIDAndKey([]byte(*serverConfirmation.PairID), generatePairConnKey(pairKey, []byte(accessKey)))
	pairConnBuilder.SetRendezvousServer(NatRendezvousServer)
	pairConnBuilder.SetTLS(serviceConfig.Cert)
	//pairConnBuilder.SetForceRelay() // TODO: add setting for this
	pairConnBuilder.SetRelayTemp(relayURL, &peerDeviceID)
	err = doPair(pairConnBuilder)
	if err != nil {
		return err
	}
	return nil
}
