package repo

import (
	"strings"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
)

func Machine(ip string, inv *talos.Inventory) config.Machine {
	m := config.Machine{IP: ip, MAC: inv.PrimaryMAC(), UUID: inv.UUID, Arch: config.Arch(inv.Arch), CPUs: inv.CPUs, MemBytes: inv.MemoryBytes, KVM: inv.KVM, TPM: inv.TPM, Watchdog: inv.Watchdog,
		Virtual: inv.Virtual || talos.IsVirtual(inv.Manufacturer, inv.Product), Model: strings.TrimSpace(inv.Manufacturer + " " + inv.Product)}
	if m.Arch == "" {
		m.Arch = config.ArchAMD64
	}
	for _, d := range inv.InstallCandidates() {
		m.Disks = append(m.Disks, config.MachineDisk{DevPath: d.DevPath, SizeBytes: d.SizeBytes, Transport: d.Transport})
	}
	return m
}
