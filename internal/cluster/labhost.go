package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

// LabDial opens SSH to a lab host by its machine row (address = the row's IP).
func (m *Manager) LabDial(ctx context.Context, host *store.Machine) (*labhost.Client, error) {
	if host.LabHost == nil {
		return nil, fmt.Errorf("%s is not a lab host", host.MAC)
	}
	priv, _, err := m.Store.SSHKey(ctx)
	if err != nil {
		return nil, err
	}
	return labhost.Dial(ctx, host.IP, priv)
}

// LabVMName is the VM's libvirt name for a machine row (kept in the hostname column
// until Talos names it).
func LabVMName(vm *store.Machine) string {
	if vm.Hostname != "" && strings.HasPrefix(vm.Hostname, "vm-") {
		return vm.Hostname
	}
	return "vm-" + strings.ReplaceAll(vm.MAC[9:], ":", "")
}

// labDiskBoot flips a lab VM to boot from its disk once Talos has been told to
// install: the post-install reboot must land in the installed system.
// labDiskBoot switches a lab VM's persistent domain to disk boot. It returns
// (false, nil) for a machine that is not a lab VM (bare metal), so the caller can tell
// a real switch from a no-op.
func (m *Manager) labDiskBoot(ctx context.Context, n storeMachineRef) (bool, error) {
	vm, err := m.Store.GetMachine(ctx, n.MAC)
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

type storeMachineRef struct{ MAC string }

func labVMNameFrom(host, vm *store.Machine) string {
	if host.LabHost != nil {
		for _, v := range host.LabHost.VMs {
			if strings.EqualFold(v.MAC, vm.MAC) {
				return v.Name
			}
		}
	}
	return LabVMName(vm)
}
