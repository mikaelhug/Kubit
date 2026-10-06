package config

import (
	"fmt"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

type Storage struct {
	SystemDisk    bool   `yaml:"systemDisk,omitempty" json:"systemDisk,omitempty"`
	EphemeralSize string `yaml:"ephemeralSize,omitempty" json:"ephemeralSize,omitempty"`
}

const (
	DefaultEphemeralSize = "40GiB"
	MinEphemeralBytes    = 10 << 30
	MinSystemDataSize    = "10GiB"
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
	return c.Spec.Storage.SystemDisk && c.Spec.Platform.Longhorn.Enabled && len(n.DataDisks) == 0
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
		if len(n.DataDisks) > 0 || c.Spec.Storage.SystemDisk {
			out = append(out, n)
		}
	}
	return out
}

func (c *Cluster) LonghornReplicas() int {
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
