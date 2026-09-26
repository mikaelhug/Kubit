package labhost

import (
	"context"
	"fmt"

	"github.com/mikael/kubit/internal/factory"
)

const (
	DriverLibvirt = "libvirt"
	DriverVFKit   = "vfkit"
)

type Boot struct {
	Kernel string
	Initrd string
	ISO    string
}

func BootCacheName(version, schematic string) (string, error) {
	if len(schematic) < 12 {
		return "", fmt.Errorf("bad schematic %q", schematic)
	}
	return version + "-" + schematic[:12], nil
}

type Driver interface {
	Capacity(ctx context.Context) (Capacity, error)
	EnsureTalosBoot(ctx context.Context, f *factory.Client, schematic, version, arch string) (Boot, error)
	Define(ctx context.Context, s VMSpec) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string, force bool) error
	Delete(ctx context.Context, name string) error
	Resize(ctx context.Context, name string, cpus, memMiB int) error
	List(ctx context.Context) ([]VM, error)
	SetDiskBoot(ctx context.Context, name string) error
	SetTalosBoot(ctx context.Context, name string, b Boot, arch string) error
	Metrics(ctx context.Context) (Metrics, error)
	Close() error
}

type Updater interface {
	CheckUpdates(ctx context.Context) (Updates, error)
}

type Router interface {
	EnsureRouted(ctx context.Context) error
}

type Identity interface {
	HostMAC(ctx context.Context) (string, error)
}

type Autostarter interface {
	Autostart(ctx context.Context) error
}

type Capacity struct {
	CPUs       int    `json:"cpus"`
	MemMiB     int    `json:"memMiB"`
	DiskGiB    int    `json:"diskGiB"`
	KVM        bool   `json:"kvm"`
	Kernel     string `json:"kernel"`
	Libvirt    string `json:"libvirt"`
	Hostname   string `json:"hostname"`
	Arch       string `json:"arch"`
	Bridge     string `json:"bridge"`
	Ready      bool   `json:"ready"`
	CheckedAt  string `json:"checkedAt"`
	Model      string `json:"model,omitempty"`
	OS         string `json:"os,omitempty"`
	Hypervisor string `json:"hypervisor,omitempty"`
	ReserveMiB int    `json:"reserveMiB,omitempty"`
	Problem    string `json:"problem,omitempty"`
	Command    string `json:"command,omitempty"`
}

const defaultReserveMiB = 2048

func (c Capacity) Reserve() int {
	if c.ReserveMiB > 0 {
		return c.ReserveMiB
	}
	return defaultReserveMiB
}
