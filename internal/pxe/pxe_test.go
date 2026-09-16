package pxe

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/mikael/kubit/internal/factory"
)

func discover(t *testing.T, mods ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	m, err := dhcpv4.NewDiscovery(net.HardwareAddr{0xe4, 0x54, 0xe8, 1, 2, 3}, mods...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBootFileByArchitecture(t *testing.T) {
	cases := map[iana.Arch]string{iana.INTEL_X86PC: FileBIOS, iana.EFI_X86_64: FileX64, iana.EFI_BC: FileX64, iana.EFI_ARM64: FileARM64}
	for arch, want := range cases {
		m := discover(t, dhcpv4.WithOption(dhcpv4.OptClientArch(arch)))
		if got, ok := bootFile(m); !ok || got != want {
			t.Errorf("arch %v: got %q ok=%v want %q", arch, got, ok, want)
		}
	}
	if _, ok := bootFile(discover(t)); ok {
		t.Error("no arch option must not be answered")
	}
}

func TestIPXEDetection(t *testing.T) {
	if isIPXE(discover(t)) {
		t.Error("plain firmware is not iPXE")
	}
	if !isIPXE(discover(t, dhcpv4.WithUserClass("iPXE", false))) {
		t.Error("user class iPXE")
	}
	if !isIPXE(discover(t, dhcpv4.WithGeneric(dhcpv4.GenericOptionCode(175), []byte{1}))) {
		t.Error("option 175 marks iPXE")
	}
}

// fakeConn records what the proxy would send.
type fakeConn struct {
	net.PacketConn
	sent []byte
	to   net.Addr
}

func (f *fakeConn) WriteTo(b []byte, a net.Addr) (int, error) {
	f.sent, f.to = b, a
	return len(b), nil
}

func TestProxyReplyOffersBootFileWithoutAddress(t *testing.T) {
	c := Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0)}
	m := discover(t, dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient:Arch:00007:UNDI:003016")), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)))
	conn := &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, m)
	if conn.sent == nil {
		t.Fatal("no reply")
	}
	reply, err := dhcpv4.FromBytes(conn.sent)
	if err != nil {
		t.Fatal(err)
	}
	if reply.MessageType() != dhcpv4.MessageTypeOffer || !reply.YourIPAddr.IsUnspecified() {
		t.Errorf("must OFFER without an address: %s", reply.Summary())
	}
	if reply.BootFileName != FileX64 || !reply.ServerIPAddr.Equal(c.IP) || reply.ClassIdentifier() != "PXEClient" {
		t.Errorf("boot pointers wrong: file=%q siaddr=%s class=%q", reply.BootFileName, reply.ServerIPAddr, reply.ClassIdentifier())
	}
	if udp, ok := conn.to.(*net.UDPAddr); !ok || !udp.IP.Equal(net.IPv4bcast) {
		t.Errorf("unaddressed client must be answered by broadcast, got %v", conn.to)
	}

	// A non-PXE DHCP client must be ignored: this is a proxy, never the LAN's DHCP server.
	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, discover(t))
	if conn.sent != nil {
		t.Error("answered an ordinary DHCP client")
	}

	// iPXE itself gets the HTTP script, not the binary again.
	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 68}, discover(t, dhcpv4.WithUserClass("iPXE", false), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64))))
	reply, _ = dhcpv4.FromBytes(conn.sent)
	if reply == nil || reply.BootFileName != "http://10.0.0.2:8069/boot.ipxe" {
		t.Errorf("iPXE must be chained to the script, got %v", reply)
	}
}

func TestBootScript(t *testing.T) {
	s := &Server{
		Config:  Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0)},
		Profile: Profile{SchematicID: "abc", TalosVersion: "v1.14.0"},
		Cache:   NewCache(t.TempDir()),
		Factory: factory.New(),
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	body := func(path string) string {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if got := body("/boot.ipxe"); !strings.Contains(got, "chain http://10.0.0.2:8069/boot.ipxe?arch=${buildarch}") {
		t.Errorf("bare script must chain with the architecture:\n%s", got)
	}
	got := body("/boot.ipxe?arch=x86_64")
	for _, want := range []string{"#!ipxe", "kernel http://10.0.0.2:8069/assets/abc/v1.14.0/kernel-amd64", "initrd http://10.0.0.2:8069/assets/abc/v1.14.0/initramfs-amd64.xz", "talos.platform=metal", "\nboot\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("script lacks %q:\n%s", want, got)
		}
	}
	if got := body("/boot.ipxe?arch=arm64"); !strings.Contains(got, "kernel-arm64") {
		t.Errorf("arm64: %s", got)
	}
	if res, _ := http.Get(ts.URL + "/assets/abc/v1.14.0/etc-passwd"); res.StatusCode != http.StatusNotFound {
		t.Errorf("only kernel/initramfs may be proxied, got %d", res.StatusCode)
	}
}

func TestMembersGetNoOffer(t *testing.T) {
	c := Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0), Decide: func(mac string) string {
		if mac == "52:54:00:4b:49:01" {
			return "local"
		}
		return ""
	}}
	m := discover(t, dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient:Arch:00007:UNDI:003016")), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)))
	m.ClientHWAddr = net.HardwareAddr{0x52, 0x54, 0x00, 0x4b, 0x49, 0x01}
	conn := &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, m)
	if conn.sent != nil {
		t.Fatal("a cluster member must not be offered a boot file")
	}
	m.ClientHWAddr = net.HardwareAddr{0x52, 0x54, 0x00, 0x4b, 0x49, 0x02}
	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4zero, Port: 68}, m)
	if conn.sent == nil {
		t.Fatal("an unknown machine (daemon undecided) must still be offered Talos")
	}
}
