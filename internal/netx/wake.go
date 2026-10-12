package netx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
)

func Wake(ctx context.Context, mac string, addrs []string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return err
	}
	if len(hw) != 6 {
		return fmt.Errorf("%s is not an Ethernet MAC", mac)
	}
	packet := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		packet = append(packet, hw...)
	}
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = setBroadcast(int(fd)) }); err != nil {
			return err
		}
		return serr
	}}
	conn, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()
	var errs []error
	for _, a := range addrs {
		dst, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(a, "9"))
		if err == nil {
			_, err = conn.WriteTo(packet, dst)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(addrs) {
		return errors.Join(errs...)
	}
	return nil
}
