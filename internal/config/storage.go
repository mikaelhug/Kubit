package config

import (
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
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

func (c *Cluster) SharesSystemDisk(n Node) bool {
	return c.Spec.Storage.SystemDisk && c.Spec.Platform.Longhorn.Enabled && len(n.DataDisks) == 0
}

const MaxDataDisks = 8

type InstallDisk struct {
	Path     string        `yaml:"path,omitempty" json:"path,omitempty"`
	Selector *DiskSelector `yaml:"selector,omitempty" json:"selector,omitempty"`
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

func HasSystemVolume(machineConfig []byte) bool {
	cfg, err := configloader.NewFromBytes(machineConfig)
	if err != nil {
		return false
	}
	for _, d := range cfg.Documents() {
		if v, ok := d.(*block.UserVolumeConfigV1Alpha1); ok && v.MetaName == SystemDataVolume {
			return true
		}
	}
	return false
}

func CheckChange(old, next *Cluster, installed, split map[string]bool) error {
	anySplit := false
	for _, s := range split {
		anySplit = anySplit || s
	}
	if anySplit && old.Spec.Storage != next.Spec.Storage {
		return fmt.Errorf("storage must stay: nodes hold a system-disk volume")
	}
	for _, nn := range next.Spec.Nodes {
		if installed[nn.IP] && !split[nn.IP] && next.SharesSystemDisk(nn) {
			return fmt.Errorf("%s must keep its system disk to Talos: it was installed without a storage volume", nn.Hostname)
		}
	}
	return nil
}
