package config

import (
	"fmt"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/imager/quirks"
)

type Storage struct {
	SystemDisk    bool   `yaml:"systemDisk,omitempty" json:"systemDisk,omitempty"`
	EphemeralSize string `yaml:"ephemeralSize,omitempty" json:"ephemeralSize,omitempty"`
}

type NodeStorage struct {
	SystemDisk *bool `yaml:"systemDisk,omitempty" json:"systemDisk,omitempty"`
}

const (
	DefaultEphemeralSize = "40GiB"
	MinEphemeralBytes    = 10 << 30
	MinSystemDataSize    = "10GiB"
	LonghornReservedPct  = 5
	SystemDataVolume     = "data-system"
)

func (s Storage) EphemeralBytes() (uint64, error) {
	var size block.Size
	if err := size.UnmarshalText([]byte(s.EphemeralSize)); err != nil {
		return 0, err
	}
	if size.IsRelative() || size.IsNegative() {
		return 0, fmt.Errorf("must be an absolute size like 40GiB")
	}
	return size.Value(), nil
}

func (s Storage) CheckEphemeral() error {
	b, err := s.EphemeralBytes()
	if err != nil {
		return err
	}
	if b < MinEphemeralBytes {
		return fmt.Errorf("at least 10GiB")
	}
	return nil
}

func (c *Cluster) SharesSystemDisk(n Node) bool {
	return c.Spec.Platform.Longhorn.Enabled && len(n.DataDisks) == 0 && c.UsesSystemDisk(n)
}

func (c *Cluster) UsesSystemDisk(n Node) bool {
	if n.Storage != nil && n.Storage.SystemDisk != nil {
		return *n.Storage.SystemDisk
	}
	return c.Spec.Storage.SystemDisk
}

func (c *Cluster) SetSystemDisk(n *Node, on bool) {
	n.Storage = nil
	if on != c.Spec.Storage.SystemDisk {
		n.Storage = &NodeStorage{SystemDisk: &on}
	}
}

func (c *Cluster) SystemDataBytes(diskBytes uint64) uint64 {
	var data block.Size
	_ = data.UnmarshalText([]byte(MinSystemDataSize))
	need := c.SystemDiskBytes()
	if diskBytes < need {
		return 0
	}
	return diskBytes - need + data.Value()
}

func UsableByReplicas(capacities []uint64, upTo int) []uint64 {
	out := make([]uint64, upTo)
	for r := range out {
		out[r] = UsableBytes(capacities, r+1)
	}
	return out
}

func UsableBytes(capacities []uint64, replicas int) uint64 {
	if replicas < 1 || len(capacities) < replicas {
		return 0
	}
	var total uint64
	for _, c := range capacities {
		total += c
	}
	fits := func(d uint64) bool {
		var sum uint64
		for _, c := range capacities {
			sum += min(c, d)
		}
		return sum >= d*uint64(replicas)
	}
	lo, hi := uint64(0), total/uint64(replicas)
	for lo < hi {
		mid := hi - (hi-lo)/2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func (c *Cluster) SystemDiskBytes() uint64 {
	s := c.Spec.Storage
	if s.EphemeralSize == "" {
		s.EphemeralSize = DefaultEphemeralSize
	}
	eph, err := s.EphemeralBytes()
	if err != nil {
		return 0
	}
	var data block.Size
	_ = data.UnmarshalText([]byte(MinSystemDataSize))
	p := quirks.New(c.Spec.TalosVersion).PartitionSizes()
	return p.UKIEFISize() + p.METASize() + p.StateSize() + eph + data.Value()
}

func (c *Cluster) RunsWorkloads(n Node) bool {
	return n.Role != RoleControlPlane || c.Spec.ControlPlane.AllowScheduling == nil || *c.Spec.ControlPlane.AllowScheduling
}

const MaxDataDisks = 8

type InstallDisk struct {
	Path     string        `yaml:"path,omitempty" json:"path,omitempty"`
	Selector *DiskSelector `yaml:"selector,omitempty" json:"selector,omitempty"`
}

func (d InstallDisk) String() string {
	if d.Path != "" || d.Selector == nil {
		return d.Path
	}
	var parts []string
	if d.Selector.Type != "" {
		parts = append(parts, d.Selector.Type)
	}
	if d.Selector.Model != "" {
		parts = append(parts, d.Selector.Model)
	}
	if d.Selector.MinSize != "" {
		parts = append(parts, "≥ "+d.Selector.MinSize)
	}
	if len(parts) == 0 {
		return "first disk"
	}
	return strings.Join(parts, " ")
}

type DiskSelector struct {
	MinSize string `yaml:"minSize,omitempty" json:"minSize,omitempty"`
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
}

var LonghornExtensions = []string{"siderolabs/iscsi-tools", "siderolabs/util-linux-tools"}

func (c *Cluster) LonghornNodes() []Node {
	var out []Node
	for _, n := range c.Spec.Nodes {
		if c.RunsWorkloads(n) && (len(n.DataDisks) > 0 || c.UsesSystemDisk(n)) {
			out = append(out, n)
		}
	}
	return out
}

func (c *Cluster) LonghornReplicas() int {
	if r := c.Spec.Platform.Longhorn.Replicas; r > 0 {
		return r
	}
	return c.AutoReplicas()
}

func (c *Cluster) AutoReplicas() int {
	n := len(c.LonghornNodes())
	if n > 3 {
		return 3
	}
	if n < 1 {
		return 1
	}
	return n
}

func LayoutChange(applied, next []byte) (string, error) {
	a, err := layout(applied)
	if err != nil {
		return "", err
	}
	b, err := layout(next)
	if err != nil {
		return "", err
	}
	for _, name := range []string{constants.StatePartitionLabel, constants.EphemeralPartitionLabel, SystemDataVolume} {
		if a[name] != b[name] {
			return name, nil
		}
	}
	return "", nil
}

func layout(machineConfig []byte) (map[string]string, error) {
	cfg, err := configloader.NewFromBytes(machineConfig)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range cfg.Documents() {
		var name string
		switch v := d.(type) {
		case *block.VolumeConfigV1Alpha1:
			name = v.MetaName
		case *block.UserVolumeConfigV1Alpha1:
			name = v.MetaName
		default:
			continue
		}
		b, err := encoder.NewEncoder(d, encoder.WithComments(encoder.CommentsDisabled)).Encode()
		if err != nil {
			return nil, err
		}
		out[name] = string(b)
	}
	return out, nil
}
