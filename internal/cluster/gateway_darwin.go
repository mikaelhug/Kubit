package cluster

import (
	"net"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func defaultRoute() (string, string) {
	rib, err := route.FetchRIB(0, route.RIBTypeRoute, 0)
	if err != nil {
		return "", ""
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return "", ""
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || len(rm.Addrs) < 2 || rm.Flags&unix.RTF_GATEWAY == 0 || rm.Flags&unix.RTF_IFSCOPE != 0 {
			continue
		}
		dst, ok := rm.Addrs[0].(*route.Inet4Addr)
		if !ok || dst.IP != [4]byte{} {
			continue
		}
		if gw, ok := rm.Addrs[1].(*route.Inet4Addr); ok {
			iface := ""
			if i, err := net.InterfaceByIndex(rm.Index); err == nil {
				iface = i.Name
			}
			return net.IP(gw.IP[:]).String(), iface
		}
	}
	return "", ""
}
