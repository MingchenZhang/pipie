// +build windows

package pairconnect

import "syscall"

func setReusableFD(network, address string, c syscall.RawConn) error {
	return nil
}
