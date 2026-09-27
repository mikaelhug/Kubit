package pxe

import "golang.org/x/sys/unix"

func bindDevice(fd int, iface string) error {
	return unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface)
}
