package netx

import "net/netip"

func Broadcast(pfx netip.Prefix) netip.Addr {
	b := pfx.Masked().Addr().As4()
	host := uint32(1)<<(32-pfx.Bits()) - 1
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) | host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
