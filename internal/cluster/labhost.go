package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func (m *Manager) LabSSH(ctx context.Context, host *store.Machine) (*labhost.Client, error) {
	if host.LabHost == nil {
		return nil, fmt.Errorf("%s is not a lab host", host.MAC)
	}
	if d := host.LabHost.Driver; d != "" && d != labhost.DriverLibvirt {
		return nil, fmt.Errorf("%s is not a Debian lab host", host.MAC)
	}
	priv, _, err := m.Store.SSHKey(ctx)
	if err != nil {
		return nil, err
	}
	return labhost.Dial(ctx, host.IP, priv)
}

func (m *Manager) LabDial(ctx context.Context, host *store.Machine) (labhost.Driver, error) {
	if host.LabHost == nil {
		return nil, fmt.Errorf("%s is not a lab host", host.MAC)
	}
	switch host.LabHost.Driver {
	case "", labhost.DriverLibvirt:
		return m.LabSSH(ctx, host)
	case labhost.DriverVFKit:
		if m.Local == nil {
			return nil, fmt.Errorf("VMs on this machine are not supported here")
		}
		return m.Local()
	}
	return nil, fmt.Errorf("unknown lab host driver %q", host.LabHost.Driver)
}

func labVMName(vm *store.Machine) string {
	if vm.Hostname != "" && strings.HasPrefix(vm.Hostname, "vm-") {
		return vm.Hostname
	}
	return "vm-" + strings.ReplaceAll(vm.MAC[9:], ":", "")
}

func (m *Manager) labDiskBoot(ctx context.Context, mac string) (bool, error) {
	vm, err := m.Store.GetMachine(ctx, mac)
	if err != nil || vm.Host == "" {
		return false, nil
	}
	host, err := m.Store.GetMachine(ctx, vm.Host)
	if err != nil {
		return false, err
	}
	c, err := m.LabDial(ctx, host)
	if err != nil {
		return false, err
	}
	defer c.Close()
	return true, c.SetDiskBoot(ctx, labVMNameFrom(host, vm))
}

func labVMNameFrom(host, vm *store.Machine) string {
	if host.LabHost != nil {
		for _, v := range host.LabHost.VMs {
			if strings.EqualFold(v.MAC, vm.MAC) {
				return v.Name
			}
		}
	}
	return labVMName(vm)
}
