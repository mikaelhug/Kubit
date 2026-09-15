package config

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Machine is what the designer knows about a candidate: the discovery inventory
// reduced to what drives placement.
type Machine struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	UUID     string `json:"uuid,omitempty"`
	Arch     Arch   `json:"arch"`
	CPUs     int    `json:"cpus"`
	MemBytes uint64 `json:"memBytes"`
	KVM      bool   `json:"kvm"`
	// Virtual: a VM. Several VMs usually share one host, so control planes prefer
	// bare metal.
	Virtual bool `json:"virtual"`
	// Disks are install candidates, largest first (dev path and size).
	Disks []MachineDisk `json:"disks"`
	Model string        `json:"model,omitempty"`
}

type MachineDisk struct {
	DevPath   string `json:"devPath"`
	SizeBytes uint64 `json:"sizeBytes"`
	Transport string `json:"transport,omitempty"`
}

// Design proposes a cluster declaration for a set of machines: which become control
// planes, hostnames, install disks, and a MetalLB range. Every choice is a plain field
// the wizard lets the operator override.
func Design(name string, machines []Machine, opts DesignOptions) (*Cluster, []Warning) {
	topo := Recommend(len(machines))
	ordered := append([]Machine(nil), machines...)
	// Control planes: bare metal before VMs (a hypervisor is one failure domain),
	// then the most alike, smallest machines; KVM-capable ones are worth more as
	// workers (runsc-kvm) when there are workers to be had.
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
	c.Spec.Platform = Platform{MetalLB: MetalLB{Enabled: true}, IngressNginx: Addon{Enabled: true}, GVisor: Addon{Enabled: true}, MetricsServer: Addon{Enabled: true}}
	sched := topo.AllowScheduling
	c.Spec.ControlPlane.AllowScheduling = &sched
	cps, workers := 0, 0
	for i, mch := range ordered {
		n := Node{IP: mch.IP, MAC: mch.MAC, UUID: mch.UUID, Arch: mch.Arch, KVM: mch.KVM}
		if i < topo.ControlPlanes {
			cps++
			n.Pool, n.Hostname = "controlplane", fmt.Sprintf("%s-cp-%02d", name, cps)
		} else {
			workers++
			n.Pool, n.Hostname = "worker", fmt.Sprintf("%s-worker-%02d", name, workers)
		}
		if len(mch.Disks) > 0 {
			n.InstallDisk = InstallDisk{Path: mch.Disks[0].DevPath}
		}
		c.Spec.Nodes = append(c.Spec.Nodes, n)
	}
	if opts.MetalLBRange != "" {
		c.Spec.Platform.MetalLB.Range = opts.MetalLBRange
	} else if len(machines) > 0 {
		if a, err := netip.ParseAddr(machines[0].IP); err == nil && a.Is4() {
			b := a.As4()
			c.Spec.Platform.MetalLB.Range = fmt.Sprintf("%d.%d.%d.200-%d.%d.%d.220", b[0], b[1], b[2], b[0], b[1], b[2])
		}
	}
	if topo.HA && len(machines) > 0 {
		if a, err := netip.ParseAddr(machines[0].IP); err == nil && a.Is4() {
			b := a.As4()
			c.Spec.ControlPlane.VIP = fmt.Sprintf("%d.%d.%d.250", b[0], b[1], b[2])
		}
	}
	c.applyDefaults()
	return c, Lint(c, machines)
}

type DesignOptions struct {
	MetalLBRange string
}

// Warning is a lint finding: something legal that an operator should know before
// creating the cluster.
type Warning struct {
	Level   string `json:"level"` // info | warn
	Code    string `json:"code"`
	Message string `json:"message"`
	Node    string `json:"node,omitempty"`
}

// Lint reports design smells on a declaration; machines (optional) add hardware checks.
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
	for _, n := range c.ControlPlanes() {
		if m, ok := byMAC[strings.ToLower(n.MAC)]; ok && m.Virtual {
			virtualCPs++
		}
	}
	if virtualCPs >= 2 {
		warn("warn", "control-planes-on-vms", "", "%d control planes are virtual machines; if they share a hypervisor, one host failure takes etcd quorum with it. Spread them over hosts or use bare metal.", virtualCPs)
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
			if m.MemBytes > 0 && m.MemBytes < 2<<30 {
				warn("warn", "low-memory", n.Hostname, "%s has %d MiB RAM; Talos control planes need at least 2 GiB.", n.Hostname, m.MemBytes>>20)
			}
			if n.Role == RoleControlPlane && m.KVM && len(c.Workers()) > 0 {
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
		} else if a, err := netip.ParseAddr(n.IP); err == nil && a.Is4() && !subnet.IsValid() {
			subnet = netip.PrefixFrom(a, 24).Masked()
		}
	}
	if m := c.Spec.Platform.MetalLB; m.Enabled && subnet.IsValid() {
		if lo, hi, err := ParseIPRange(m.Range); err == nil {
			if !subnet.Contains(lo) || !subnet.Contains(hi) {
				warn("warn", "metallb-off-subnet", "", "MetalLB range %s is outside %s; Layer-2 announcements only work inside the nodes' subnet.", m.Range, subnet)
			}
			for _, n := range c.Spec.Nodes {
				if a, err := netip.ParseAddr(n.IP); err == nil && !a.Less(lo) && !hi.Less(a) {
					warn("warn", "metallb-overlaps-node", n.Hostname, "%s's address %s lies inside the MetalLB range.", n.Hostname, n.IP)
				}
			}
		}
	}
	if len(c.Spec.Nodes) > 0 && !c.Spec.Platform.MetricsServer.Enabled {
		warn("info", "no-metrics", "", "metrics-server is off: no CPU/memory usage in Kubit or kubectl top.")
	}
	return out
}

// Overlaps reports which stored clusters' MetalLB ranges intersect this one's.
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
