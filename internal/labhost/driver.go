package labhost

import "context"

const (
	DriverLibvirt = "libvirt"
	DriverVFKit   = "vfkit"
)

type Boot struct {
	Kernel string
	Initrd string
	ISO    string
}

type Driver interface {
	Capacity(ctx context.Context) (Capacity, error)
	EnsureTalosBoot(ctx context.Context, factoryURL, schematic, version, arch string) (Boot, error)
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

const DefaultReserveMiB = 2048

func (c Capacity) Reserve() int {
	if c.ReserveMiB > 0 {
		return c.ReserveMiB
	}
	return DefaultReserveMiB
}

var (
	_ Driver  = (*Client)(nil)
	_ Updater = (*Client)(nil)
	_ Router  = (*Client)(nil)
)
