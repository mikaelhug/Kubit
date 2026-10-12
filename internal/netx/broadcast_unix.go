//go:build darwin || linux

package netx

import "syscall"

func setBroadcast(fd int) error {
	return syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
