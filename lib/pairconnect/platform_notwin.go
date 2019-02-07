// +build !windows

package pairconnect

import "syscall"

func setReusableFD(network, address string, c syscall.RawConn) error {
	errChan := make(chan error)
	c.Control(func(fd uintptr) {
		err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if err != nil {
			log.Notice("socket set SO_REUSEADDR failed")
			errChan <- err
			return
		}
		// SO_REUSEPORT
		err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 0xf, 1)
		if err != nil {
			log.Notice("socket set SO_REUSEPORT failed")
			errChan <- err
			return
		}
		errChan <- nil
	})
	return <-errChan
}
