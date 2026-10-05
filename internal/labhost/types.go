package labhost

import (
	"fmt"
	"slices"
)

type VMSpec struct {
	Name    string    `json:"name"`
	MAC     string    `json:"mac"`
	CPUs    int       `json:"cpus"`
	MemMiB  int       `json:"memMiB"`
	DiskGiB int       `json:"diskGiB"`
	DataGiB int       `json:"dataGiB,omitempty"`
	Kernel  string    `json:"-"`
	Initrd  string    `json:"-"`
	ISO     string    `json:"-"`
	Arch    string    `json:"-"`
	Bridge  string    `json:"-"`
	Routed  bool      `json:"-"`
	TCG     bool      `json:"-"`
	System  Placement `json:"-"`
	Data    Placement `json:"-"`
}

type VM struct {
	Name    string   `json:"name"`
	MAC     string   `json:"mac"`
	State   string   `json:"state"`
	CPUs    int      `json:"cpus"`
	MemMiB  int      `json:"memMiB"`
	DiskGiB int      `json:"diskGiB"`
	DataGiB int      `json:"dataGiB,omitempty"`
	Boot    string   `json:"boot"`
	IP      string   `json:"ip,omitempty"`
	Disks   []VMDisk `json:"disks,omitempty"`
}

func MAC(host, n int) string { return fmt.Sprintf("52:54:00:6b:%02x:%02x", host&0xff, n&0xff) }

func SerialConsole(arch string) string {
	if arch == "arm64" {
		return "console=ttyAMA0"
	}
	return "console=ttyS0"
}

func TalosKernelArgs(consoles ...string) []string {
	return slices.Concat([]string{"talos.platform=metal"}, consoles, []string{"init_on_alloc=1", "slab_nomerge", "pti=on"})
}

type Metrics struct {
	Load1      float64     `json:"load1"`
	CPUPct     float64     `json:"cpuPct"`
	MemUsed    int64       `json:"memUsed"`
	MemTotal   int64       `json:"memTotal"`
	DiskUsed   int64       `json:"diskUsed"`
	DiskTotal  int64       `json:"diskTotal"`
	VMsRunning int         `json:"vmsRunning"`
	UptimeSec  int64       `json:"uptimeSec"`
	At         string      `json:"at"`
	Pools      []PoolUsage `json:"pools,omitempty"`
}

type Updates struct {
	Count           int    `json:"count"`
	Security        int    `json:"security"`
	RebootRequired  bool   `json:"rebootRequired"`
	KernelRunning   string `json:"kernelRunning"`
	KernelInstalled string `json:"kernelInstalled"`
	Release         string `json:"release"`
	Unattended      bool   `json:"unattended"`
	CheckedAt       string `json:"checkedAt"`
}

func (u Updates) NeedsReboot() bool {
	return u.RebootRequired || (u.KernelInstalled != "" && u.KernelRunning != "" && u.KernelInstalled != u.KernelRunning)
}
