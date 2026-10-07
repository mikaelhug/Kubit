package talos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Inventory struct {
	IP            string         `json:"ip"`
	Hostname      string         `json:"hostname,omitempty"`
	TalosVersion  string         `json:"talosVersion"`
	Arch          string         `json:"arch"`
	Platform      string         `json:"platform"`
	Stage         string         `json:"stage"`
	CPUs          int            `json:"cpus"`
	CPUModel      string         `json:"cpuModel,omitempty"`
	CPUCores      int            `json:"cpuCores,omitempty"`
	CPUSockets    int            `json:"cpuSockets,omitempty"`
	MemoryBytes   uint64         `json:"memoryBytes"`
	Memory        []MemoryModule `json:"memory,omitempty"`
	Manufacturer  string         `json:"manufacturer,omitempty"`
	Product       string         `json:"product,omitempty"`
	SystemVersion string         `json:"systemVersion,omitempty"`
	BIOSVersion   string         `json:"biosVersion,omitempty"`
	UUID          string         `json:"uuid,omitempty"`
	Serial        string         `json:"serial,omitempty"`
	KVM           bool           `json:"kvm"`
	Virtual       bool           `json:"virtual"`
	Disks         []Disk         `json:"disks"`
	Links         []Link         `json:"links"`
	PCI           []PCIDevice    `json:"pci,omitempty"`
	Gateway       string         `json:"gateway,omitempty"`
	Nameservers   []string       `json:"nameservers,omitempty"`
	BootTime      string         `json:"bootTime,omitempty"`
	Extensions    []Extension    `json:"extensions,omitempty"`
	Etcd          *EtcdMember    `json:"etcd,omitempty"`
}

func (inv Inventory) MarshalJSON() ([]byte, error) {
	type plain Inventory
	p := plain(inv)
	if p.Disks == nil {
		p.Disks = []Disk{}
	}
	if p.Links == nil {
		p.Links = []Link{}
	}
	return json.Marshal(p)
}

type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Author  string `json:"author,omitempty"`
}

type EtcdMember struct {
	MemberID     string   `json:"memberId"`
	Leader       bool     `json:"leader"`
	Learner      bool     `json:"learner"`
	DBSizeBytes  int64    `json:"dbSizeBytes"`
	DBInUseBytes int64    `json:"dbInUseBytes"`
	RaftIndex    uint64   `json:"raftIndex"`
	RaftTerm     uint64   `json:"raftTerm"`
	Errors       []string `json:"errors,omitempty"`
}

type Disk struct {
	DevPath    string   `json:"devPath"`
	SizeBytes  uint64   `json:"sizeBytes"`
	Model      string   `json:"model,omitempty"`
	Transport  string   `json:"transport,omitempty"`
	Rotational bool     `json:"rotational"`
	Readonly   bool     `json:"readonly"`
	CDROM      bool     `json:"cdrom"`
	Serial     string   `json:"serial,omitempty"`
	WWID       string   `json:"wwid,omitempty"`
	Firmware   string   `json:"firmware,omitempty"`
	Links      []string `json:"links,omitempty"`
}

type Link struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses,omitempty"`
	SpeedMbps int      `json:"speedMbps,omitempty"`
	MTU       uint32   `json:"mtu,omitempty"`
}

type PCIDevice struct {
	Address string `json:"address"`
	Kind    string `json:"kind,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
	Driver  string `json:"driver,omitempty"`
}

// PCI-SIG base class codes: mass storage, network, display, processing accelerator.
var pciClasses = []string{"0x01", "0x02", "0x03", "0x12"}

func (c *Client) Inspect(ctx context.Context) (*Inventory, error) {
	ctx = c.nodeContext(ctx)
	inv := &Inventory{IP: c.IP}

	v, err := c.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("version: %w", err)
	}
	if len(v.Messages) > 0 {
		inv.TalosVersion = v.Messages[0].Version.Tag
		inv.Arch = v.Messages[0].Version.Arch
		inv.Platform = v.Messages[0].Platform.Name
	}
	if stage, err := c.Stage(ctx); err == nil {
		inv.Stage = stage
	}
	if hn, err := safe.StateGetByID[*network.HostnameStatus](ctx, c.COSI, network.HostnameID); err == nil {
		inv.Hostname = hn.TypedSpec().Hostname
	}
	if cpu, err := c.MachineClient.CPUInfo(ctx, &emptypb.Empty{}); err == nil && len(cpu.Messages) > 0 {
		inv.CPUs = len(cpu.Messages[0].CpuInfo)
		if inv.CPUs > 0 {
			inv.CPUModel = strings.TrimSpace(cpu.Messages[0].CpuInfo[0].ModelName)
		}
	}
	if cores, err := safe.StateListAll[*hardware.CPUCore](ctx, c.COSI); err == nil {
		sockets := map[string]bool{}
		for core := range cores.All() {
			inv.CPUCores++
			sockets[core.TypedSpec().Socket] = true
		}
		inv.CPUSockets = len(sockets)
	}
	if mem, err := c.Memory(ctx); err == nil && len(mem.Messages) > 0 && mem.Messages[0].Meminfo != nil {
		inv.MemoryBytes = mem.Messages[0].Meminfo.Memtotal * 1024
	}
	if si, err := safe.StateGetByID[*hardware.SystemInformation](ctx, c.COSI, hardware.SystemInformationID); err == nil {
		inv.Manufacturer = si.TypedSpec().Manufacturer
		inv.Product = si.TypedSpec().ProductName
		inv.Virtual = IsVirtual(inv.Manufacturer, inv.Product)
		inv.UUID = si.TypedSpec().UUID
		inv.Serial = si.TypedSpec().SerialNumber
		inv.SystemVersion = dmiText(si.TypedSpec().Version)
		inv.BIOSVersion = dmiText(si.TypedSpec().BIOSVersion)
	}
	inv.Memory = c.memoryModules(ctx)
	inv.KVM = c.exists(ctx, "/dev/kvm")

	disks, err := safe.StateListAll[*block.Disk](ctx, c.COSI)
	if err != nil {
		return nil, fmt.Errorf("disks: %w", err)
	}
	for d := range disks.All() {
		s := d.TypedSpec()
		if strings.HasPrefix(s.DevPath, "/dev/loop") || strings.HasPrefix(s.DevPath, "/dev/zram") {
			continue
		}
		inv.Disks = append(inv.Disks, Disk{
			DevPath: s.DevPath, SizeBytes: s.Size, Model: s.Model, Transport: s.Transport,
			Rotational: s.Rotational, Readonly: s.Readonly, CDROM: s.CDROM,
			Serial: s.Serial, WWID: s.WWID, Firmware: s.FirmwareVersion, Links: byIDLinks(s.Symlinks),
		})
	}
	sort.Slice(inv.Disks, func(i, j int) bool { return inv.Disks[i].DevPath < inv.Disks[j].DevPath })

	addrs := map[string][]string{}
	if al, err := safe.StateListAll[*network.AddressStatus](ctx, c.COSI); err == nil {
		for a := range al.All() {
			addrs[a.TypedSpec().LinkName] = append(addrs[a.TypedSpec().LinkName], a.TypedSpec().Address.String())
		}
	}
	links, err := safe.StateListAll[*network.LinkStatus](ctx, c.COSI)
	if err != nil {
		return nil, fmt.Errorf("links: %w", err)
	}
	for l := range links.All() {
		s := l.TypedSpec()
		if !s.Physical() {
			continue
		}
		inv.Links = append(inv.Links, Link{
			Name: l.Metadata().ID(), MAC: s.HardwareAddr.String(),
			Up: s.OperationalState.String() == "up", Addresses: addrs[l.Metadata().ID()],
			SpeedMbps: max(s.SpeedMegabits, 0), MTU: s.MTU,
		})
	}
	sort.Slice(inv.Links, func(i, j int) bool { return inv.Links[i].Name < inv.Links[j].Name })
	if pci, err := safe.StateListAll[*hardware.PCIDevice](ctx, c.COSI); err == nil {
		for d := range pci.All() {
			s := d.TypedSpec()
			if !slices.Contains(pciClasses, s.ClassID) {
				continue
			}
			kind := s.Subclass
			if kind == "" {
				kind = s.Class
			}
			inv.PCI = append(inv.PCI, PCIDevice{Address: d.Metadata().ID(), Kind: kind, Vendor: s.Vendor, Product: s.Product, Driver: s.Driver})
		}
		sort.Slice(inv.PCI, func(i, j int) bool { return inv.PCI[i].Address < inv.PCI[j].Address })
	}
	inv.Gateway, inv.Nameservers = c.uplinkRoute(ctx)

	if st, err := c.MachineClient.SystemStat(ctx, &emptypb.Empty{}); err == nil && len(st.Messages) > 0 && st.Messages[0].BootTime > 0 {
		inv.BootTime = time.Unix(int64(st.Messages[0].BootTime), 0).UTC().Format(time.RFC3339)
	}
	if exts, err := safe.StateListAll[*runtime.ExtensionStatus](ctx, c.COSI); err == nil {
		for e := range exts.All() {
			m := e.TypedSpec().Metadata
			inv.Extensions = append(inv.Extensions, Extension{Name: m.Name, Version: m.Version, Author: m.Author})
		}
		sort.Slice(inv.Extensions, func(i, j int) bool { return inv.Extensions[i].Name < inv.Extensions[j].Name })
	}
	return inv, nil
}

func (c *Client) EtcdMemberInfo(ctx context.Context) (*EtcdMember, error) {
	st, err := c.EtcdStatus(c.nodeContext(ctx))
	if err != nil {
		return nil, err
	}
	for _, m := range st.Messages {
		ms := m.MemberStatus
		if ms == nil {
			continue
		}
		return &EtcdMember{
			MemberID: fmt.Sprintf("%x", ms.MemberId), Leader: ms.Leader == ms.MemberId, Learner: ms.IsLearner,
			DBSizeBytes: ms.DbSize, DBInUseBytes: ms.DbSizeInUse, RaftIndex: ms.RaftIndex, RaftTerm: ms.RaftTerm, Errors: ms.Errors,
		}, nil
	}
	return nil, errors.New("no etcd member status")
}

func (inv *Inventory) PrimaryMAC() string {
	for _, l := range inv.Links {
		for _, a := range l.Addresses {
			if strings.HasPrefix(a, inv.IP+"/") {
				return l.MAC
			}
		}
	}
	if len(inv.Links) > 0 {
		return inv.Links[0].MAC
	}
	return ""
}

func byIDLinks(symlinks []string) []string {
	var out []string
	for _, l := range symlinks {
		if id, ok := strings.CutPrefix(l, "/dev/disk/by-id/"); ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (inv *Inventory) InstallCandidates() []Disk {
	var out []Disk
	for _, d := range inv.Disks {
		if d.DevPath == "" || d.Readonly || d.CDROM || d.Transport == "usb" {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rotational != out[j].Rotational {
			return !out[i].Rotational
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	return out
}

func (c *Client) uplinkRoute(ctx context.Context) (string, []string) {
	var gateway string
	if routes, err := safe.StateListAll[*network.RouteStatus](ctx, c.COSI); err == nil {
		for r := range routes.All() {
			s := r.TypedSpec()
			if s.Table == nethelpers.TableMain && s.Gateway.Is4() && (!s.Destination.IsValid() || s.Destination.Bits() == 0) {
				gateway = s.Gateway.String()
				break
			}
		}
	}
	var servers []string
	if res, err := safe.StateGetByID[*network.ResolverStatus](ctx, c.COSI, network.ResolverID); err == nil {
		for _, a := range res.TypedSpec().DNSServers {
			servers = append(servers, a.String())
		}
	}
	return gateway, servers
}

func (c *Client) exists(ctx context.Context, path string) bool {
	stream, err := c.LS(ctx, &machine.ListRequest{Root: path})
	if err != nil {
		return false
	}
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return status.Code(err) != codes.Unknown && status.Code(err) != codes.NotFound
		}
	}
}

func IsVirtual(manufacturer, product string) bool {
	m := strings.ToLower(manufacturer + " " + product)
	for _, hint := range []string{"qemu", "kvm", "vmware", "virtualbox", "innotek", "xen", "virtual machine", "apple virtualization", "parallels", "bochs", "proxmox", "hetzner vserver", "openstack", "amazon ec2", "google compute engine", "nutanix"} {
		if strings.Contains(m, hint) {
			return true
		}
	}
	return false
}

func (inv *Inventory) UplinkAddress() string {
	for _, l := range inv.Links {
		for _, a := range l.Addresses {
			if strings.HasPrefix(a, inv.IP+"/") {
				return a
			}
		}
	}
	return ""
}
