package pxe

import (
	"net"

	"golang.org/x/sys/unix"
)

func bindDevice(fd int, iface string) error {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return err
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, ifc.Index)
}
