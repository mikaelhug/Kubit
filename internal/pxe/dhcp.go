// Package pxe boots bare-metal machines into Talos maintenance mode with nothing but a
// network cable: a proxyDHCP responder (never an address server — the LAN's DHCP keeps
// handing out leases), a TFTP server for the iPXE binaries, and an HTTP server for the
// iPXE script and a cache of Image Factory boot assets.
package pxe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/insomniacslk/dhcp/iana"
)

// Boot files served over TFTP, by client architecture (DHCP option 93).
const (
	FileBIOS  = "undionly.kpxe"
	FileX64   = "ipxe.efi"
	FileARM64 = "ipxe-arm64.efi"
)

type Config struct {
	// Interface to answer on; its IPv4 address becomes next-server and the HTTP host.
	Interface string
	IP        net.IP
	HTTPPort  int
	Log       *log.Logger
}

// bootFile picks the iPXE binary for the firmware that is asking.
func bootFile(m *dhcpv4.DHCPv4) (string, bool) {
	for _, a := range m.ClientArch() {
		switch a {
		case iana.INTEL_X86PC:
			return FileBIOS, true
		case iana.EFI_X86_64, iana.EFI_BC, iana.EFI_IA32:
			return FileX64, true
		case iana.EFI_ARM64:
			return FileARM64, true
		}
	}
	return "", false
}

// isIPXE reports whether the request comes from iPXE itself (option 77 "iPXE" or the
// option 175 it always sends), which must be pointed at the script instead of looping
// back into the binary.
func isIPXE(m *dhcpv4.DHCPv4) bool {
	for _, uc := range m.UserClass() {
		if uc == "iPXE" {
			return true
		}
	}
	return m.Options.Has(dhcpv4.GenericOptionCode(175))
}

// ScriptURL is where iPXE fetches its boot script.
func (c Config) ScriptURL() string {
	return fmt.Sprintf("http://%s:%d/boot.ipxe", c.IP, c.HTTPPort)
}

// handle answers PXE clients only. Two sockets see traffic: :67 (broadcast DISCOVER
// from firmware, alongside the real DHCP server) and :4011 (the PXE boot-server
// REQUEST the firmware sends to us after it got its lease).
func (c Config) handle(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4) {
	if m.OpCode != dhcpv4.OpcodeBootRequest {
		return
	}
	pxeClient := strings.HasPrefix(m.ClassIdentifier(), "PXEClient")
	if !pxeClient && !isIPXE(m) {
		return
	}
	var (
		file string
		ok   bool
	)
	if isIPXE(m) {
		file, ok = c.ScriptURL(), true
	} else {
		file, ok = bootFile(m)
	}
	if !ok {
		c.Log.Printf("pxe: %s asks with unsupported architecture %v", m.ClientHWAddr, m.ClientArch())
		return
	}
	var mt dhcpv4.MessageType
	switch m.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		mt = dhcpv4.MessageTypeOffer
	case dhcpv4.MessageTypeRequest:
		mt = dhcpv4.MessageTypeAck
	default:
		return
	}
	reply, err := dhcpv4.NewReplyFromRequest(m,
		dhcpv4.WithMessageType(mt),
		dhcpv4.WithServerIP(c.IP),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(c.IP)),
		dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient")),
		dhcpv4.WithOption(dhcpv4.OptTFTPServerName(c.IP.String())),
		dhcpv4.WithOption(dhcpv4.OptBootFileName(file)),
		// PXE vendor options (43): discovery control bit 3 = "use boot filename as is",
		// which spares the firmware a boot-server menu round trip.
		dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.OptionVendorSpecificInformation, []byte{6, 1, 8, 0xff})),
	)
	if err != nil {
		c.Log.Printf("pxe: build reply: %v", err)
		return
	}
	reply.BootFileName = file
	reply.ServerHostName = c.IP.String()
	// yiaddr stays 0.0.0.0: the proxy never assigns addresses.
	dest := peer
	if m.GatewayIPAddr != nil && !m.GatewayIPAddr.IsUnspecified() {
		dest = &net.UDPAddr{IP: m.GatewayIPAddr, Port: dhcpv4.ServerPort}
	} else if udp, ok := peer.(*net.UDPAddr); ok && (udp.IP.IsUnspecified() || udp.IP == nil) {
		dest = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}
	if _, err := conn.WriteTo(reply.ToBytes(), dest); err != nil {
		c.Log.Printf("pxe: reply to %s: %v", m.ClientHWAddr, err)
		return
	}
	c.Log.Printf("pxe: %s (%v) → %s", m.ClientHWAddr, m.ClientArch(), file)
}

// ServeDHCP runs the proxyDHCP responders on :67 and :4011 until ctx ends.
func (c Config) ServeDHCP(ctx context.Context) error {
	var servers []*server4.Server
	for _, port := range []int{dhcpv4.ServerPort, 4011} {
		s, err := server4.NewServer(c.Interface, &net.UDPAddr{IP: net.IPv4zero, Port: port}, c.handle)
		if err != nil {
			return fmt.Errorf("listen udp :%d (needs root): %w", port, err)
		}
		servers = append(servers, s)
	}
	errc := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *server4.Server) { errc <- s.Serve() }(s)
	}
	select {
	case <-ctx.Done():
		for _, s := range servers {
			s.Close()
		}
		return nil
	case err := <-errc:
		for _, s := range servers {
			s.Close()
		}
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}
