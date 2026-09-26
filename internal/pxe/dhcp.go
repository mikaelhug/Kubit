package pxe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"syscall"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/insomniacslk/dhcp/iana"
)

const (
	FileBIOS  = "undionly.kpxe"
	FileX64   = "ipxe.efi"
	FileARM64 = "ipxe-arm64.efi"
)

type Config struct {
	onDHCP               func(mac, arch string)
	onLog                func(line string)
	onPlainDHCP          func(mac, class string)
	Decide               func(mac string) string
	KubitURL, KubitToken string
	HTTPOnly             bool
	Interface            string
	IP                   net.IP
	HTTPPort             int
	Log                  *log.Logger
}

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

func isIPXE(m *dhcpv4.DHCPv4) bool {
	for _, uc := range m.UserClass() {
		if uc == "iPXE" {
			return true
		}
	}
	return m.Options.Has(dhcpv4.GenericOptionCode(175))
}

func (c Config) ScriptURL() string {
	return fmt.Sprintf("http://%s:%d/boot.ipxe", c.IP, c.HTTPPort)
}

func (c Config) handle(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4) {
	if m.OpCode != dhcpv4.OpcodeBootRequest {
		return
	}
	mac, ipxe := m.ClientHWAddr.String(), isIPXE(m)
	if !ipxe && !strings.HasPrefix(m.ClassIdentifier(), "PXEClient") {
		if c.onPlainDHCP != nil && m.MessageType() == dhcpv4.MessageTypeDiscover {
			c.onPlainDHCP(mac, m.ClassIdentifier())
		}
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
	if c.decide(mac) == "local" {
		c.Log.Printf("pxe: %s: no offer, boots from its own disk", mac)
		c.logf("%s: no offer, boots from its own disk (Kubit's decision)", mac)
		return
	}
	file, ok := c.ScriptURL(), true
	if !ipxe {
		file, ok = bootFile(m)
	}
	if !ok {
		c.Log.Printf("pxe: %s asks with unsupported architecture %v", mac, m.ClientArch())
		c.logf("%s network-boots with an architecture Kubit has no iPXE for (%v)", mac, m.ClientArch())
		return
	}
	mods := []dhcpv4.Modifier{
		dhcpv4.WithMessageType(mt),
		dhcpv4.WithServerIP(c.IP),
		dhcpv4.WithClientIP(m.ClientIPAddr),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(c.IP)),
		dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient")),
		dhcpv4.WithOption(dhcpv4.OptTFTPServerName(c.IP.String())),
		dhcpv4.WithOption(dhcpv4.OptBootFileName(file)),
		dhcpv4.WithOptionCopied(m, dhcpv4.OptionClientMachineIdentifier),
	}
	if ipxe || file == FileBIOS {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.OptionVendorSpecificInformation, []byte{6, 1, 8, 0xff})))
	}
	reply, err := dhcpv4.NewReplyFromRequest(m, mods...)
	if err != nil {
		c.Log.Printf("pxe: build reply: %v", err)
		return
	}
	reply.BootFileName = file
	reply.ServerHostName = c.IP.String()
	dest := peer
	if m.GatewayIPAddr != nil && !m.GatewayIPAddr.IsUnspecified() {
		dest = &net.UDPAddr{IP: m.GatewayIPAddr, Port: dhcpv4.ServerPort}
	} else if udp, ok := peer.(*net.UDPAddr); ok && (udp.IP == nil || udp.IP.IsUnspecified()) {
		dest = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	}
	if _, err := conn.WriteTo(reply.ToBytes(), dest); err != nil {
		c.Log.Printf("pxe: reply to %s: %v", mac, err)
		return
	}
	c.Log.Printf("pxe: %s (%v) → %s", mac, m.ClientArch(), file)
	if c.onDHCP != nil {
		arch := ""
		switch file {
		case FileBIOS, FileX64:
			arch = "amd64"
		case FileARM64:
			arch = "arm64"
		}
		c.onDHCP(mac, arch)
	}
	c.logf("%s (%v) offered %s", mac, m.ClientArch(), file)
}

func (c Config) logf(format string, args ...any) {
	if c.onLog != nil {
		c.onLog(fmt.Sprintf(format, args...))
	}
}

func (c Config) decide(mac string) string {
	if c.Decide == nil {
		return "talos"
	}
	if d := c.Decide(strings.ToLower(mac)); d != "" {
		return d
	}
	return "local"
}

func listenShared(ctx context.Context, port int) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = setReuse(int(fd))
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, err
	}
	return pc, nil
}

func (c Config) ServeDHCP(ctx context.Context) error {
	var servers []*server4.Server
	for _, port := range []int{dhcpv4.ServerPort, 4011} {
		conn, err := listenShared(ctx, port)
		if err != nil {
			return fmt.Errorf("listen udp :%d (needs root): %w", port, err)
		}
		s, err := server4.NewServer(c.Interface, nil, c.handle, server4.WithConn(conn))
		if err != nil {
			conn.Close()
			return err
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
