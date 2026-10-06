package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Machine struct {
	IP       string
	MAC      string
	UUID     string
	Arch     Arch
	CPUs     int
	MemBytes uint64
	KVM      bool
	Virtual  bool
	Disks    []MachineDisk
}

type MachineDisk struct {
	DevPath   string
	SizeBytes uint64
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
	c.Spec.Platform = Platform{MetricsServer: Addon{Enabled: true}, CertManager: Addon{Enabled: true}, Flux: Flux{Enabled: true}}
	if opts.MetalLBRange != "" {
		c.Spec.Platform.MetalLB = MetalLB{Enabled: true, Range: opts.MetalLBRange}
		c.Spec.Platform.Traefik.Enabled = true
	}
	auto := topo.ControlPlanes
	for _, mch := range ordered {
		if opts.Roles[mch.MAC] == RoleControlPlane {
			auto--
		}
	}
	roles := map[string]Role{}
	for _, mch := range ordered {
		role := opts.Roles[mch.MAC]
		if role == "" {
			role = RoleWorker
			if auto > 0 {
				role = RoleControlPlane
				auto--
			}
		}
		roles[mch.MAC] = role
	}
	place := func(mch Machine, role Role) {
		n := c.NodeFor(mch, c.NextHostname(role), role)
		n.Network = opts.Networks[mch.MAC]
		c.Spec.Nodes = append(c.Spec.Nodes, n)
	}
	for _, want := range []Role{RoleControlPlane, RoleWorker} {
		for _, mch := range ordered {
			if role := roles[mch.MAC]; role == want || (want == RoleWorker && role != RoleControlPlane) {
				place(mch, role)
			}
		}
	}
	cps := len(c.ControlPlanes())
	sched := topo.AllowScheduling || cps == len(c.Spec.Nodes)
	c.Spec.ControlPlane.AllowScheduling = &sched
	if cps >= 3 && len(machines) > 0 {
		c.Spec.ControlPlane.VIP = opts.VIP
		if lan, ok := Slash24(machines[0].IP); ok && opts.VIP == "" {
			c.Spec.ControlPlane.VIP = hostIn(lan, 250)
		}
	}
	c.applyDefaults()
	return c, Lint(c, machines)
}

func (c *Cluster) NodeFor(m Machine, hostname string, role Role) Node {
	n := Node{Hostname: hostname, IP: m.IP, MAC: m.MAC, UUID: m.UUID, Arch: m.Arch, Role: role, KVM: m.KVM && c.Spec.Platform.GVisor.Enabled}
	if len(m.Disks) > 0 {
		n.InstallDisk = InstallDisk{Path: m.Disks[0].DevPath}
	}
	return n
}

const MaxNameLen = 50

var nonLabel = regexp.MustCompile(`[^a-z0-9]+`)

func NameFrom(s string) string {
	n := strings.Trim(nonLabel.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > MaxNameLen {
		n = strings.TrimRight(n[:MaxNameLen], "-")
	}
	return n
}

func CheckName(name string) error {
	if hostnameRE.MatchString(name) && len(name) <= MaxNameLen {
		return nil
	}
	hint := "lowercase letters, digits and hyphens, at most " + strconv.Itoa(MaxNameLen) + " characters"
	if s := NameFrom(name); s != "" {
		hint += fmt.Sprintf(", e.g. %q", s)
	}
	return fmt.Errorf("cluster name %q: use %s", name, hint)
}

func (c *Cluster) NextHostname(role Role) string {
	kind := "worker"
	if role == RoleControlPlane {
		kind = "cp"
	}
	for i := 1; ; i++ {
		if h := fmt.Sprintf("%s-%s-%02d", c.Metadata.Name, kind, i); c.NodeIndex(h) < 0 {
			return h
		}
	}
}

type DesignOptions struct {
	MetalLBRange string
	Roles        map[string]Role
	VIP          string
	Networks     map[string]*NodeNetwork
}

const (
	MinWorkerBytes       = 1500 << 20
	MinControlPlaneBytes = 1600 << 20
)

type Warning struct {
	Level   string
	Code    string
	Message string
	Node    string
}

func Lint(c *Cluster, machines []Machine) []Warning {
	var out []Warning
	warn := func(level, code, node, format string, args ...any) {
		out = append(out, Warning{Level: level, Code: code, Node: node, Message: fmt.Sprintf(format, args...)})
	}
	cps := c.ControlPlanes()
	if len(cps) == 1 {
		warn("warn", "single-control-plane", "", "One control plane: no HA, and losing it loses the cluster. Use 3 for etcd quorum.")
		if c.Spec.ControlPlane.VIP == "" && (cps[0].Network == nil || len(cps[0].Network.Addresses) == 0) {
			warn("warn", "dhcp-endpoint", cps[0].Hostname, "The API endpoint follows the control plane's DHCP lease (%s); reserve it or give the node a static address.", cps[0].IP)
		}
	}
	if v, err := netip.ParseAddr(c.Spec.ControlPlane.VIP); err == nil && len(cps) > 0 {
		if lan, ok := Slash24(cps[0].TargetIP()); ok && !lan.Contains(v) {
			warn("warn", "vip-subnet", "", "The VIP %s is outside %s, the control planes' network; Talos only moves a VIP within one subnet.", v, lan)
		}
	}
	if len(cps) >= 3 && c.Spec.ControlPlane.VIP == "" {
		warn("warn", "no-vip", "", "No control plane VIP: the API endpoint follows %s; if it fails, kubeconfig and joining nodes lose the API.", cps[0].IP)
	}
	byRole := map[Role]map[Arch]bool{}
	for _, n := range c.Spec.Nodes {
		if byRole[n.Role] == nil {
			byRole[n.Role] = map[Arch]bool{}
		}
		byRole[n.Role][n.Arch] = true
	}
	for role, archs := range byRole {
		if len(archs) > 1 {
			warn("warn", "mixed-arch", "", "The %s nodes mix architectures; images and scheduling differ per arch.", role)
		}
	}
	byMAC := map[string]Machine{}
	for _, m := range machines {
		byMAC[strings.ToLower(m.MAC)] = m
	}
	virtualCPs := 0
	for _, n := range cps {
		m, ok := byMAC[strings.ToLower(n.MAC)]
		if ok && m.Virtual {
			virtualCPs++
		}
		if ok && m.MemBytes > 0 && m.MemBytes < MinControlPlaneBytes {
			warn("warn", "control-plane-undersized", n.Hostname, "Control plane %s has under ~1.6 GiB usable RAM; give it a 2 GiB machine (provisioning will refuse it).", n.Hostname)
		}
	}
	if virtualCPs >= 2 {
		warn("warn", "control-planes-on-vms", "", "%d control planes are virtual machines; if they share a hypervisor, one host failure takes etcd quorum with it.", virtualCPs)
	}
	for _, n := range c.Spec.Nodes {
		m, ok := byMAC[strings.ToLower(n.MAC)]
		if !ok {
			continue
		}
		if len(m.Disks) == 0 {
			warn("warn", "no-disk", n.Hostname, "%s has no install disk candidate.", n.Hostname)
		} else if m.Disks[0].SizeBytes < 20<<30 {
			warn("warn", "small-disk", n.Hostname, "%s: largest disk is %d GiB; Talos wants 10 GiB plus room for images and etcd.", n.Hostname, m.Disks[0].SizeBytes>>30)
		}
		if n.Role == RoleWorker && c.Spec.Platform.AddOns() && m.MemBytes > 0 && m.MemBytes < MinWorkerBytes {
			warn("warn", "worker-undersized", n.Hostname, "Worker %s has under ~1.5 GiB usable RAM; give it a 2 GiB machine (provisioning will refuse it).", n.Hostname)
		} else if m.MemBytes > 0 && m.MemBytes < 2<<30 {
			warn("warn", "low-memory", n.Hostname, "%s has %d MiB RAM; 2 GiB is the floor for a %s.", n.Hostname, m.MemBytes>>20, map[Role]string{RoleControlPlane: "control plane", RoleWorker: "worker"}[n.Role])
		}
	}
	if m := c.Spec.Platform.MetalLB; m.Enabled && len(c.Spec.Nodes) > 0 {
		subnet, ok := Slash24(c.Spec.Nodes[0].IP)
		if lo, hi, err := ParseIPRange(m.Range); ok && err == nil {
			if !subnet.Contains(lo) || !subnet.Contains(hi) {
				warn("warn", "metallb-off-subnet", "", "MetalLB range %s is outside %s; Layer-2 announcements only work inside the nodes' subnet.", m.Range, subnet)
			}
			for _, n := range c.Spec.Nodes {
				if a, err := netip.ParseAddr(n.IP); err == nil && InRange(a, lo, hi) {
					warn("warn", "metallb-overlaps-node", n.Hostname, "%s's address %s lies inside the MetalLB range.", n.Hostname, n.IP)
				}
			}
		}
	}
	return out
}

func Slash24(ip string) (netip.Prefix, bool) {
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

func InRange(a, lo, hi netip.Addr) bool {
	return !a.Less(lo) && !hi.Less(a)
}
