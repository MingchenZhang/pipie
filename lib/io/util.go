package io

import (
	"bufio"
	"encoding/json"
	"github.com/op/go-logging"
	"io"
	"net"
	"strings"
	"time"
)

var log, _ = logging.GetLogger("io")

const (
	ModeStringLen         = 14
	ModePortForwardServer = "MODE_PORT_SERV"
	ModePortForwardClient = "MODE_PORT_CLIE"
	ModePipeInlet         = "MODE_PIPE_INLE"
	ModePipeOutlet        = "MODE_PIPE_OUTL"
)

const (
	protocolVersion int = 1
)

type signalDealer struct {
	reader *bufio.Reader
	writer *bufio.Writer
}

type PortForwardSignal struct {
	Version int                   `json:"version"`
	Type    PortForwardSignalType `json:"type"`
	Code    PortForwardSignalCode `json:"code,omitempty"`
	//DestHost   string                `json:"target_ip,omitempty"`
	//DestPort int                   `json:"target_port,omitempty"`
}

func errorAssert(err error) {
	if err != nil {
		panic(err)
	}
}

func ReaderToConn(reader io.Reader, conn net.Conn, checkWrite bool) {
	var buf = [2048]byte{}
	for {
		read, err := reader.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				// disconnected
				log.Debugf("io.EOF")
				return
			} else if strings.Contains(err.Error(), "use of closed network connection") {
				// has been called Close
				log.Debugf("use of closed network connection")
				return
			} else if strings.Contains(err.Error(), "broken pipe") {
				log.Debugf("pipe is broken")
				return
			} else if strings.Contains(err.Error(), "file already closed") {
				return
				log.Debugf("file already closed")
			} else {
				log.Error(err)
				return
			}
		}
		if checkWrite {
			var buf = []byte{0} // smux bug: when read to zero byte slice to detect connection closing, smux read return nil error directly
			conn.SetReadDeadline(time.Now())
			if _, err := conn.Read(buf); err == io.EOF {
				// conn has been closed
				log.Debugf("checkWrite found conn has been closed")
				return
			}
		}
		var written = 0
		for {
			if written == read {
				break
			}
			write, err := conn.Write(buf[written:read])
			if err != nil {
				log.Error(err)
				return
			}
			if write == 0 {
				log.Error("write zero byte")
				return
			}
			written += write
		}
	}
}

func ReaderToWriter(reader io.Reader, writer io.Writer) {
	var buf = [2048]byte{}
	for {
		read, err := reader.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				// disconnected
				return
			} else if strings.Contains(err.Error(), "use of closed network connection") {
				// has been called Close
				return
			} else if strings.Contains(err.Error(), "broken pipe") {
				return
			} else if strings.Contains(err.Error(), "file already closed") {
				return
			} else {
				log.Error(err)
				return
			}
		}
		var written = 0
		for {
			if written == read {
				break
			}
			write, err := writer.Write(buf[written:read])
			if err != nil {
				log.Error(err)
				return
			}
			if write == 0 {
				log.Error("write zero byte")
				return
			}
			written += write
		}
	}
}

func (c signalDealer) GetSignal() (*PortForwardSignal, error) {
	var signalBytes, err = c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var signal PortForwardSignal
	if err = json.Unmarshal(signalBytes, &signal); err != nil {
		return nil, err
	}
	return &signal, nil
}

func (c signalDealer) SendSignal(signal *PortForwardSignal) error {
	var signalBytes, err = json.Marshal(signal)
	if err != nil {
		return err
	}
	_, err = c.writer.Write(append(signalBytes[:], []byte("\n")[:]...))
	if err != nil {
		panic(err) // shouldn't happen at all
	}
	err = c.writer.Flush()
	if err != nil {
		panic(err) // shouldn't happen at all
	}
	return nil
}
