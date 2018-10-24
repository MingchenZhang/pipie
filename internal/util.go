package internal

import (
	"bufio"
	"encoding/json"
	"github.com/xtaci/smux"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type PortForwardSignal struct {
	Version    int                   `json:"version"`
	Type       PortForwardSignalType `json:"type"`
	Code       PortForwardSignalCode `json:"code,omitempty"`
	TargetIP   string                `json:"target_ip,omitempty"`
	TargetPort int                   `json:"target_port,omitempty"`
}

type signalDealer struct {
	reader *bufio.Reader
	writer *bufio.Writer
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

func (c signalDealer) SendSignal(signal *PortForwardSignal) (error) {
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

func errorAssert(err error) {
	if err != nil {
		panic(err)
	}
}

func portPlusOne(addressString string) string {
	localAddrSlice := strings.Split(addressString, ":")
	portNum, err := strconv.ParseInt(localAddrSlice[1], 10, 32)
	errorAssert(err)
	portPlusOne := localAddrSlice[0] + ":" + strconv.Itoa(int(portNum+1))
	return portPlusOne
}

func portPlusOneUDPAddr(addr net.Addr) net.Addr {
	localAddrSlice := strings.Split(addr.String(), ":")
	portNum, err := strconv.ParseInt(localAddrSlice[1], 10, 32)
	errorAssert(err)
	portPlusOne := localAddrSlice[0] + ":" + strconv.Itoa(int(portNum+1))
	result, err := net.ResolveUDPAddr("udp", portPlusOne)
	errorAssert(err)
	return result
}

type connWrapper struct {
	conn       net.Conn
	afterClose func()
	beforeClose func()
}

type muxSession interface {
	AcceptStream() (*smux.Stream, error)
	OpenStream() (*smux.Stream, error)
	Close() error
}

func (c connWrapper) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}
func (c connWrapper) Write(b []byte) (n int, err error) {
	return c.conn.Write(b)
}
func (c connWrapper) Close() error {
	if c.beforeClose != nil {
		c.beforeClose()
	}
	if c.afterClose != nil {
		defer c.afterClose()
	}
	return c.conn.Close()
}
func (c connWrapper) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}
func (c connWrapper) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}
func (c connWrapper) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}
func (c connWrapper) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}
func (c connWrapper) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
