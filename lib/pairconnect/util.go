package pairconnect

import (
	"net"
	"time"
)

const (
	traversalAttemptRetry = 4
	traversalTimeout      = 8
)

func errorAssert(err error) {
	if err != nil {
		panic(err)
	}
}

type MuxSession interface {
	Accept() (net.Conn, error)
	Open() (net.Conn, error)
	Close() error
}

type connWrapper struct {
	conn        net.Conn
	afterClose  func()
	beforeClose func()
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
