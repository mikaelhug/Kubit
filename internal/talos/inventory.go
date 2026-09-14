package talos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Inventory struct {
	IP           string `json:"ip"`
	Hostname     string `json:"hostname,omitempty"`
	TalosVersion string `json:"talosVersion"`
	Arch         string `json:"arch"`
	Platform     string `json:"platform"`
	Stage        string `json:"stage"` // maintenance, running, ...
	CPUs         int    `json:"cpus"`
	MemoryBytes  uint64 `json:"memoryBytes"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	UUID         string `json:"uuid,omitempty"`
	Serial       string `json:"serial,omitempty"`
	KVM          bool   `json:"kvm"`
	Disks        []Disk `json:"disks"`
	Links        []Link `json:"links"`
	// Fields below are only filled on configured (mTLS) nodes.
	BootTime   string      `json:"bootTime,omitempty"`
	Extensions []Extension `json:"extensions,omitempty"`
	Etcd       *EtcdMember `json:"etcd,omitempty"`
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
	DevPath    string `json:"devPath"`
	SizeBytes  uint64 `json:"sizeBytes"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	Rotational bool   `json:"rotational"`
	Readonly   bool   `json:"readonly"`
	CDROM      bool   `json:"cdrom"`
}

type Link struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses,omitempty"`
}

// Inspect gathers what the UI shows for a discovered node. Every call here is served in
// maintenance mode as well as on a configured node.
func (c *Client) Inspect(ctx context.Context) (*Inventory, error) {
	ctx = c.Context(ctx)
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
	if st, err := safe.StateGetByID[*runtime.MachineStatus](ctx, c.COSI, runtime.MachineStatusID); err == nil {
		inv.Stage = st.TypedSpec().Stage.String()
	}
	if hn, err := safe.StateGetByID[*network.HostnameStatus](ctx, c.COSI, network.HostnameID); err == nil {
		inv.Hostname = hn.TypedSpec().Hostname
	}
	if cpu, err := c.MachineClient.CPUInfo(ctx, &emptypb.Empty{}); err == nil && len(cpu.Messages) > 0 {
		inv.CPUs = len(cpu.Messages[0].CpuInfo)
	}
	if mem, err := c.Memory(ctx); err == nil && len(mem.Messages) > 0 && mem.Messages[0].Meminfo != nil {
		inv.MemoryBytes = mem.Messages[0].Meminfo.Memtotal * 1024
	}
	if si, err := safe.StateGetByID[*hardware.SystemInformation](ctx, c.COSI, hardware.SystemInformationID); err == nil {
		inv.Manufacturer = si.TypedSpec().Manufacturer
		inv.Product = si.TypedSpec().ProductName
		inv.UUID = si.TypedSpec().UUID
		inv.Serial = si.TypedSpec().SerialNumber
	}
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
		})
	}
	sort.Slice(inv.Links, func(i, j int) bool { return inv.Links[i].Name < inv.Links[j].Name })

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

// EtcdMemberInfo reads this node's etcd member status; only control planes answer.
func (c *Client) EtcdMemberInfo(ctx context.Context) (*EtcdMember, error) {
	st, err := c.EtcdStatus(c.Context(ctx))
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

// PrimaryMAC is the MAC of the physical link carrying the node's IP.
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

// InstallCandidates lists writable, non-removable disks, largest first.
func (inv *Inventory) InstallCandidates() []Disk {
	var out []Disk
	for _, d := range inv.Disks {
		if d.Readonly || d.CDROM || d.Transport == "usb" {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SizeBytes > out[j].SizeBytes })
	return out
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
