package cluster

import (
	"net"

	"golang.org/x/net/route"
)

func DefaultGateway() string {
	rib, err := route.FetchRIB(0, route.RIBTypeRoute, 0)
	if err != nil {
		return ""
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || len(rm.Addrs) < 2 || rm.Flags&0x2 == 0 {
			continue
		}
		dst, ok := rm.Addrs[0].(*route.Inet4Addr)
		if !ok || dst.IP != [4]byte{} {
			continue
		}
		if gw, ok := rm.Addrs[1].(*route.Inet4Addr); ok {
			return net.IP(gw.IP[:]).String()
		}
	}
	return ""
}
