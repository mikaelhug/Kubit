package config

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
)

type Machine struct {
	IP       string        `json:"ip"`
	MAC      string        `json:"mac"`
	UUID     string        `json:"uuid,omitempty"`
	Arch     Arch          `json:"arch"`
	CPUs     int           `json:"cpus"`
	MemBytes uint64        `json:"memBytes"`
	KVM      bool          `json:"kvm"`
	TPM      bool          `json:"tpm"`
	Watchdog bool          `json:"watchdog"`
	Virtual  bool          `json:"virtual"`
	Host     string        `json:"host,omitempty"`
	Disks    []MachineDisk `json:"disks"`
	Model    string        `json:"model,omitempty"`
}

type MachineDisk struct {
	DevPath   string `json:"devPath"`
	SizeBytes uint64 `json:"sizeBytes"`
	Transport string `json:"transport,omitempty"`
}

func Design(name string, machines []Machine, opts DesignOptions) (*Cluster, []Warning) {
	topo := Recommend(len(machines))
	ordered := append([]Machine(nil), machines...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Virtual != b.Virtual {
			return !a.Virtual
		}
		if topo.Workers > 0 && a.KVM != b.KVM {
			return !a.KVM
		}
		if a.MemBytes != b.MemBytes {
			return a.MemBytes < b.MemBytes
		}
		if a.CPUs != b.CPUs {
			return a.CPUs < b.CPUs
		}
		return a.IP < b.IP
	})
	c := &Cluster{APIVersion: APIVersion, Kind: KindCluster, Metadata: Metadata{Name: name}}
	c.Spec.Pools = []Pool{{Name: "controlplane", Role: RoleControlPlane}, {Name: "worker", Role: RoleWorker}}
	c.Spec.Platform = Platform{MetalLB: MetalLB{Enabled: true}, Traefik: Addon{Enabled: true}, MetricsServer: Addon{Enabled: true}, CertManager: Addon{Enabled: true}, Flux: Flux{Enabled: true}}
	sched := topo.AllowScheduling
	c.Spec.ControlPlane.AllowScheduling = &sched
	c.Spec.Network.Firewall = new(true)
	cps, workers := 0, 0
	for i, mch := range ordered {
		n := Node{IP: mch.IP, MAC: mch.MAC, UUID: mch.UUID, Arch: mch.Arch, KVM: mch.KVM, TPM: mch.TPM, Watchdog: mch.Watchdog}
		if i < topo.ControlPlanes {
			cps++
			n.Pool, n.Hostname = "controlplane", fmt.Sprintf("%s-cp-%02d", name, cps)
		} else {
			workers++
			n.Pool, n.Hostname = "worker", fmt.Sprintf("%s-worker-%02d", name, workers)
		}
		if len(mch.Disks) > 0 {
			n.InstallDisk = InstallDisk{Path: mch.Disks[0].DevPath}
			if opts.DataDisks {
				for _, d := range mch.Disks[1:] {
					n.DataDisks = append(n.DataDisks, d.DevPath)
				}
			}
		}
		c.Spec.Nodes = append(c.Spec.Nodes, n)
	}
	c.Spec.Storage.SystemDisk = true
	c.Spec.Storage.Encryption = c.DefaultEncryption()
	c.Spec.Platform.Longhorn.Enabled = len(c.LonghornNodes()) > 0
	if opts.MetalLBRange != "" {
		c.Spec.Platform.MetalLB.Range = opts.MetalLBRange
	} else if len(machines) > 0 {
		c.Spec.Platform.MetalLB.Range = DefaultMetalLBRange(machines[0].IP)
	}
	if topo.HA && len(machines) > 0 {
		if lan, ok := slash24(machines[0].IP); ok {
			c.Spec.ControlPlane.VIP = hostIn(lan, 250)
		}
	}
	c.applyDefaults()
	c.Spec.Platform.Builds.Enabled = c.Spec.Platform.Longhorn.Enabled && c.RegistryIP() != ""
	return c, Lint(c, machines)
}

type DesignOptions struct {
	MetalLBRange string
	DataDisks    bool
}

const (
	MinWorkerBytes       = 1500 << 20
	MinControlPlaneBytes = 1600 << 20
)

const talosPartitionsBytes = 3 << 30

type Warning struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Node    string `json:"node,omitempty"`
}

func Lint(c *Cluster, machines []Machine) []Warning {
	var out []Warning
	warn := func(level, code, node, format string, args ...any) {
		out = append(out, Warning{Level: level, Code: code, Node: node, Message: fmt.Sprintf(format, args...)})
	}
	cps := c.ControlPlanes()
	switch {
	case len(cps) == 1:
		warn("warn", "single-control-plane", "", "One control plane: no HA, and losing it loses the cluster. Use 3 for etcd quorum.")
	case len(cps)%2 == 0:
		warn("warn", "even-control-planes", "", "%d control planes: an even etcd member count tolerates no more failures than %d would.", len(cps), len(cps)-1)
	}
	if c.Spec.ControlPlane.AllowScheduling != nil && *c.Spec.ControlPlane.AllowScheduling && len(c.Spec.Nodes) >= 6 {
		warn("info", "schedulable-control-planes", "", "Control planes also run workloads; with %d nodes dedicated control planes are the usual choice.", len(c.Spec.Nodes))
	}
	if c.Spec.ControlPlane.AllowScheduling != nil && !*c.Spec.ControlPlane.AllowScheduling && len(c.Workers()) == 0 {
		warn("warn", "no-schedulable-nodes", "", "Control planes are dedicated and there are no workers: nothing can run pods.")
	}
	if len(cps) >= 3 && c.Spec.ControlPlane.VIP == "" {
		warn("warn", "no-vip", "", "No control plane VIP: the API endpoint follows %s; if it fails, kubeconfig and joining nodes lose the API.", cps[0].IP)
	}
	if len(cps) == 1 && c.Spec.ControlPlane.VIP == "" && (cps[0].Network == nil || len(cps[0].Network.Addresses) == 0) {
		warn("warn", "dhcp-endpoint", cps[0].Hostname, "The API endpoint follows the control plane's DHCP lease (%s); reserve it or give the node a static address so the cluster survives a lease change.", cps[0].IP)
	}
	byPool := map[string]map[Arch]bool{}
	for _, n := range c.Spec.Nodes {
		if byPool[n.Pool] == nil {
			byPool[n.Pool] = map[Arch]bool{}
		}
		byPool[n.Pool][n.Arch] = true
	}
	for pool, archs := range byPool {
		if len(archs) > 1 {
			warn("warn", "mixed-arch", "", "Pool %s mixes architectures; images and scheduling differ per arch.", pool)
		}
	}
	byMAC := map[string]Machine{}
	for _, m := range machines {
		byMAC[strings.ToLower(m.MAC)] = m
	}
	virtualCPs := 0
	hosts := map[string]bool{}
	for _, n := range c.ControlPlanes() {
		if m, ok := byMAC[strings.ToLower(n.MAC)]; ok && m.Virtual {
			virtualCPs++
			hosts[m.Host] = true
		}
	}
	if virtualCPs >= 2 {
		if len(hosts) == 1 && !hosts[""] {
			warn("info", "lab-cluster", "", "All control planes are VMs on one lab host: a lab, not HA — the host is a single failure domain.")
		} else {
			warn("warn", "control-planes-on-vms", "", "%d control planes are virtual machines; if they share a hypervisor, one host failure takes etcd quorum with it. Spread them over hosts or use bare metal.", virtualCPs)
		}
	}
	for _, n := range cps {
		if m, ok := byMAC[strings.ToLower(n.MAC)]; ok && m.MemBytes > 0 && m.MemBytes < MinControlPlaneBytes {
			warn("warn", "control-plane-undersized", n.Hostname, "Control plane %s has under ~1.6 GiB usable RAM; a control plane cannot run etcd and the API server on that — give it a 2 GiB machine (provisioning will refuse it).", n.Hostname)
		}
	}
	var subnet netip.Prefix
	for _, n := range c.Spec.Nodes {
		m, ok := byMAC[strings.ToLower(n.MAC)]
		if ok {
			if len(m.Disks) == 0 {
				warn("warn", "no-disk", n.Hostname, "%s has no install disk candidate.", n.Hostname)
			} else if m.Disks[0].SizeBytes < 20<<30 {
				warn("warn", "small-disk", n.Hostname, "%s: largest disk is %d GiB; Talos wants 10 GiB plus room for images and etcd.", n.Hostname, m.Disks[0].SizeBytes>>30)
			}
			if c.SharesSystemDisk(n) {
				eph, _ := c.Spec.Storage.EphemeralBytes()
				for _, d := range m.Disks {
					if d.DevPath == n.InstallDisk.Path && d.SizeBytes < eph+talosPartitionsBytes+(10<<30) {
						warn("warn", "small-system-disk", n.Hostname, "%s: the %d GiB system disk leaves under 10 GiB for storage after Talos and /var (%s).", n.Hostname, d.SizeBytes>>30, c.Spec.Storage.EphemeralSize)
					}
				}
			}
			for _, d := range n.DataDisks {
				if !slices.ContainsFunc(m.Disks, func(md MachineDisk) bool { return md.DevPath == d }) {
					warn("warn", "unknown-data-disk", n.Hostname, "%s: data disk %s is not in the machine's inventory; the volume stays unprovisioned until a disk matches.", n.Hostname, d)
				}
			}
			if n.Role == RoleWorker && c.Spec.Platform.AddOns() && m.MemBytes > 0 && m.MemBytes < MinWorkerBytes {
				warn("warn", "worker-undersized", n.Hostname, "Worker %s has under ~1.5 GiB usable RAM; Talos and the kubelet leave it too little for the platform add-ons — give it a 2 GiB machine (provisioning will refuse it).", n.Hostname)
			} else if m.MemBytes > 0 && m.MemBytes < 2<<30 {
				warn("warn", "low-memory", n.Hostname, "%s has %d MiB RAM; 2 GiB is the floor for a %s.", n.Hostname, m.MemBytes>>20, map[Role]string{RoleControlPlane: "control plane", RoleWorker: "worker"}[n.Role])
			}
			if c.Spec.Platform.GVisor.Enabled && n.Role == RoleControlPlane && m.KVM && len(c.Workers()) > 0 {
				warn("info", "kvm-on-control-plane", n.Hostname, "%s supports KVM; as a worker it could run runsc-kvm sandboxes.", n.Hostname)
			}
		}
		if n.Network != nil {
			for _, a := range n.Network.Addresses {
				if pfx, err := netip.ParsePrefix(a); err == nil {
					if subnet.IsValid() && !subnet.Contains(pfx.Addr()) && n.Network.VLAN == 0 {
						warn("warn", "address-off-subnet", n.Hostname, "%s: %s is outside %s where the other nodes live.", n.Hostname, a, subnet)
					}
					if !subnet.IsValid() {
						subnet = pfx.Masked()
					}
				}
			}
		} else if lan, ok := slash24(n.IP); ok && !subnet.IsValid() {
			subnet = lan
		}
	}
	if m := c.Spec.Platform.MetalLB; m.Enabled && subnet.IsValid() {
		if lo, hi, err := ParseIPRange(m.Range); err == nil {
			if !subnet.Contains(lo) || !subnet.Contains(hi) {
				warn("warn", "metallb-off-subnet", "", "MetalLB range %s is outside %s; Layer-2 announcements only work inside the nodes' subnet.", m.Range, subnet)
			}
			for _, n := range c.Spec.Nodes {
				if a, err := netip.ParseAddr(n.IP); err == nil && inRange(a, lo, hi) {
					warn("warn", "metallb-overlaps-node", n.Hostname, "%s's address %s lies inside the MetalLB range.", n.Hostname, n.IP)
				}
			}
		}
	}
	if !c.FirewallOn() {
		warn("info", "firewall-off", "", "Host firewall is off: every node port is open to the network.")
	}
	if c.Spec.Storage.Encryption == "" {
		warn("info", "encryption-off", "", "Disk encryption is off: a removed disk exposes its data.")
	}
	if c.Spec.Storage.Encryption == EncryptionTPM {
		warn("warn", "encryption-tpm-secureboot", "", "TPM sealing needs a Secure Boot image, which Kubit does not install yet.")
	}
	if len(c.Spec.Nodes) > 0 && !c.Spec.Platform.MetricsServer.Enabled {
		warn("info", "no-metrics", "", "metrics-server is off: no CPU/memory usage in Kubit or kubectl top.")
	}
	return out
}

func Overlaps(rangeSpec string, others map[string]string) []string {
	lo, hi, err := ParseIPRange(rangeSpec)
	if err != nil {
		return nil
	}
	var out []string
	for name, r := range others {
		olo, ohi, err := ParseIPRange(r)
		if err != nil {
			continue
		}
		if !hi.Less(olo) && !ohi.Less(lo) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func slash24(ip string) (netip.Prefix, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, 24).Masked(), true
}

func hostIn(lan netip.Prefix, last byte) string {
	b := lan.Addr().As4()
	b[3] = last
	return netip.AddrFrom4(b).String()
}

func DefaultMetalLBRange(ip string) string {
	lan, ok := slash24(ip)
	if !ok {
		return ""
	}
	return hostIn(lan, 200) + "-" + hostIn(lan, 220)
}

func inRange(a, lo, hi netip.Addr) bool {
	return !a.Less(lo) && !hi.Less(a)
}
