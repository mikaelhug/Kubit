package pxe

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	c.handle(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m, true)
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
	if reply.Options.Has(dhcpv4.OptionVendorSpecificInformation) {
		t.Error("UEFI firmware must not get the option 43 discovery bypass; it comes back to :4011")
	}

	conn = &fakeConn{}
	guid := append([]byte{0}, make([]byte, 16)...)
	req := discover(t, dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient:Arch:00000:UNDI:002001")), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.INTEL_X86PC)), dhcpv4.WithGeneric(dhcpv4.OptionClientMachineIdentifier, guid), dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest), dhcpv4.WithClientIP(net.IPv4(10, 0, 0, 9)))
	c.handle(conn, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 4011}, req, false)
	reply, _ = dhcpv4.FromBytes(conn.sent)
	if reply == nil || reply.MessageType() != dhcpv4.MessageTypeAck || reply.BootFileName != FileBIOS || !reply.ClientIPAddr.Equal(net.IPv4(10, 0, 0, 9)) {
		t.Errorf("boot-server request must be ACKed with the file and ciaddr: %v", reply)
	}
	if reply != nil && (!reply.Options.Has(dhcpv4.OptionVendorSpecificInformation) || string(reply.Options.Get(dhcpv4.OptionClientMachineIdentifier)) != string(guid)) {
		t.Error("BIOS reply needs the option 43 bypass and the echoed option 97")
	}

	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, discover(t), true)
	if conn.sent != nil {
		t.Error("answered an ordinary DHCP client")
	}

	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 68}, discover(t, dhcpv4.WithUserClass("iPXE", false), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64))), true)
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
	if got := body("/boot.ipxe?arch=arm64"); !strings.Contains(got, "kernel-arm64") || !strings.Contains(got, "console=ttyAMA0") || strings.Contains(got, "console=ttyS0") {
		t.Errorf("arm64: %s", got)
	}
	if got := body("/boot.ipxe?arch=x86_64"); !strings.Contains(got, "console=tty0 console=ttyS0") {
		t.Errorf("amd64 consoles: %s", got)
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
	c.handle(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m, true)
	if conn.sent != nil {
		t.Fatal("a cluster member must not be offered a boot file")
	}
	m.ClientHWAddr = net.HardwareAddr{0x52, 0x54, 0x00, 0x4b, 0x49, 0x02}
	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m, true)
	if conn.sent != nil {
		t.Fatal("a machine the daemon can't classify (unreachable) must get no offer — fail closed, don't re-image")
	}

	open := Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0)}
	conn = &fakeConn{}
	open.handle(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m, true)
	if conn.sent == nil {
		t.Fatal("with no decider configured, standalone mode must offer Talos")
	}
}

func TestTrackerNewBootAfterSilence(t *testing.T) {
	tr := newTracker()
	tr.dhcp("aa:bb:cc:dd:ee:ff", "amd64")
	tr.http("10.0.0.9", "amd64", "kernel")
	b := tr.boots["aa:bb:cc:dd:ee:ff"]
	if b.Stage != "kernel" || b.IP != "10.0.0.9" {
		t.Fatalf("first boot: %+v", b)
	}
	b.LastSeen = time.Now().Add(-2 * time.Minute)
	tr.dhcp("aa:bb:cc:dd:ee:ff", "amd64")
	if b.Stage != "dhcp" || b.IP != "" || b.Count != 1 || tr.byIP["10.0.0.9"] != "" {
		t.Errorf("a PXE request after a minute of silence starts a new boot: %+v", b)
	}
}

func pxeDiscover(t *testing.T, mods ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	return discover(t, append([]dhcpv4.Modifier{dhcpv4.WithOption(dhcpv4.OptClassIdentifier("PXEClient:Arch:00007:UNDI:003016")), dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64))}, mods...)...)
}

func TestProxyPortAnswersRequestsOnlyForItself(t *testing.T) {
	c := Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0)}
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	toRouter := pxeDiscover(t, dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest), dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(10, 0, 0, 1))))
	conn := &fakeConn{}
	c.handle(conn, bcast, toRouter, true)
	if conn.sent != nil {
		t.Error("a REQUEST to the LAN's DHCP server must not be answered on :67")
	}
	noID := pxeDiscover(t, dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest))
	conn = &fakeConn{}
	c.handle(conn, bcast, noID, true)
	if conn.sent != nil {
		t.Error("a REQUEST without a server identifier must not be answered on :67")
	}
	toUs := pxeDiscover(t, dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest), dhcpv4.WithOption(dhcpv4.OptServerIdentifier(c.IP)))
	conn = &fakeConn{}
	c.handle(conn, bcast, toUs, true)
	if reply, _ := dhcpv4.FromBytes(conn.sent); reply == nil || reply.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("a REQUEST naming this server must be ACKed, got %v", reply)
	}
	conn = &fakeConn{}
	c.handle(conn, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 4011}, toRouter, false)
	if conn.sent == nil {
		t.Error(":4011 answers every boot-server REQUEST")
	}
}

func TestFollowsTheInterfaceAddress(t *testing.T) {
	var current net.IP
	var lookups int
	lookup := func(string) (net.IP, error) {
		lookups++
		if current == nil {
			return nil, errors.New("en0 has no IPv4 address")
		}
		return current, nil
	}
	var logged []string
	a := &ifaceAddr{name: "en0", lookup: lookup, logf: func(f string, args ...any) { logged = append(logged, fmt.Sprintf(f, args...)) }}
	c := Config{HTTPPort: 8069, Log: log.New(io.Discard, "", 0), addr: a}
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	conn := &fakeConn{}
	c.handle(conn, bcast, pxeDiscover(t), true)
	if conn.sent != nil {
		t.Fatal("no answer while the interface has no address")
	}
	if c.BaseURL() != "http://:8069" || c.address() != "" {
		t.Errorf("no address: %q %q", c.BaseURL(), c.address())
	}
	current = net.IPv4(192, 168, 1, 20).To4()
	a.at = a.at.Add(-addrTTL)
	conn = &fakeConn{}
	c.handle(conn, bcast, pxeDiscover(t), true)
	reply, _ := dhcpv4.FromBytes(conn.sent)
	if reply == nil || !reply.ServerIPAddr.Equal(current) || !reply.ServerIdentifier().Equal(current) {
		t.Fatalf("answers with the new address once it appears, got %v", reply)
	}
	before := lookups
	for range 5 {
		c.ip()
	}
	if lookups != before {
		t.Errorf("the address is looked up at most every %s, not per packet (%d lookups)", addrTTL, lookups-before)
	}
	current = net.IPv4(192, 168, 1, 21).To4()
	if got := a.get(time.Now().Add(addrTTL)); !got.Equal(current) {
		t.Errorf("a changed address is picked up after the TTL, got %v", got)
	}
	if len(logged) != 3 {
		t.Errorf("each change is logged once: %q", logged)
	}
	fixed := Config{IP: net.IPv4(10, 0, 0, 2), Interface: "does-not-exist0"}
	fixed.follow()
	if fixed.addr != nil || !fixed.ip().Equal(net.IPv4(10, 0, 0, 2)) {
		t.Error("--ip pins the address")
	}
}

func TestAssetsServeOnlyTheProfileSchematic(t *testing.T) {
	var hits []string
	factorySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		fmt.Fprint(w, "KERNEL")
	}))
	defer factorySrv.Close()
	f := factory.New()
	f.SetBaseURL(factorySrv.URL)
	s := &Server{
		Config:  Config{IP: net.IPv4(10, 0, 0, 2), HTTPPort: 8069, Log: log.New(io.Discard, "", 0)},
		Profile: Profile{SchematicID: "abc123", TalosVersion: "v1.14.0"},
		Cache:   NewCache(t.TempDir()),
		Factory: f,
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	get := func(path string) int {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := get("/assets/abc123/v1.14.0/kernel-amd64"); code != http.StatusOK {
		t.Errorf("profile schematic: %d", code)
	}
	for _, p := range []string{
		"/assets/def456/v1.14.0/kernel-amd64",
		"/assets/abc123/latest/kernel-amd64",
		"/assets/abc123/v1.13.0/kernel-amd64",
		"/assets/abc123/v1.14.0/metal-amd64.iso",
		"/assets/abc123/v1.14.0/kernel-amd64.xz",
		"/assets/abc123/v1.14.0/initramfs-amd64",
		"/assets/abc123/v1.14.0/kernel-riscv64",
		"/assets/abc123/v1.14.0/kernel-amd64%2F..%2F..%2F..%2Fschematics",
		"/assets/abc123/v1.14.0/kernel-amd64%3Fx=1",
		"/assets/abc123/v1.14.0/initramfs-amd64.xz%2F..",
		"/assets/abc123/v1.14.0%2F..%2Fv1.13.0/kernel-amd64",
		"/assets/abc123/..%2Fv1.14.0/kernel-amd64",
		"/assets/debian/i386/linux",
		"/assets/debian/amd64%2F..%2F..%2F..%2Fsid/linux",
		"/assets/debian/amd64/linux%3F",
		"/assets/debian/amd64/..%2Flinux",
		"/assets/debian/amd64/vmlinuz",
		"/assets/debian/../../etc/passwd",
	} {
		if code := get(p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if len(hits) != 1 {
		t.Errorf("only the profile's asset may reach the factory: %v", hits)
	}
}

func TestRunRefusesAVersionThatIsNotARelease(t *testing.T) {
	for _, v := range []string{"", "latest", "v1.14", "v1.14.0/../x"} {
		s := &Server{Profile: Profile{SchematicID: "abc", TalosVersion: v}, Cache: NewCache(t.TempDir())}
		if err := s.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "not a release") {
			t.Errorf("%q: %v", v, err)
		}
	}
}
