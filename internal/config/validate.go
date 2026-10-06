package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/netx"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c *Cluster) Validate() error {
	var errs []error
	for _, section := range []func() []error{
		c.validateHeader, c.validateBackup, c.validatePlatform, c.validateStorage, c.validateAuth,
		c.validateTalosVersion, c.validateControlPlane, c.validateNetwork, c.validateNodes, c.validatePatches,
	} {
		errs = append(errs, section()...)
	}
	return errors.Join(errs...)
}

func (c *Cluster) validateHeader() []error {
	var errs []error
	if c.APIVersion != APIVersion || c.Kind != KindCluster {
		errs = append(errs, fmt.Errorf("expected apiVersion %s kind %s", APIVersion, KindCluster))
	}
	if !hostnameRE.MatchString(c.Metadata.Name) {
		errs = append(errs, fmt.Errorf("metadata.name %q must be a DNS label", c.Metadata.Name))
	}
	return errs
}

func (c *Cluster) validateBackup() []error {
	var errs []error
	if b := c.Spec.Backup; b.Enabled() {
		if f := strings.Fields(b.Schedule); len(f) != 5 && !(len(f) == 1 && strings.HasPrefix(f[0], "@")) {
			errs = append(errs, fmt.Errorf("backup.schedule %q: a cron schedule such as \"0 */6 * * *\"", b.Schedule))
		}
		if b.S3.Bucket == "" {
			errs = append(errs, fmt.Errorf("backup.s3.bucket is required"))
		}
		if b.S3.Endpoint != "" {
			if u, err := url.Parse(b.S3.Endpoint); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				errs = append(errs, fmt.Errorf("backup.s3.endpoint %q: an http(s) URL", b.S3.Endpoint))
			}
		}
		if len(b.AgeRecipients) == 0 {
			errs = append(errs, fmt.Errorf("backup.ageRecipients: at least one age public key, so the snapshots are encrypted"))
		}
		for _, r := range b.AgeRecipients {
			if _, err := age.ParseX25519Recipient(r); err != nil {
				errs = append(errs, fmt.Errorf("backup.ageRecipients: %q is not an age public key", r))
			}
		}
	}
	return errs
}

func (c *Cluster) validatePlatform() []error {
	var errs []error
	p := c.Spec.Platform
	if p.Longhorn.Enabled && len(c.LonghornNodes()) == 0 {
		errs = append(errs, fmt.Errorf("platform.longhorn needs storage.systemDisk or dataDisks on at least one node to hold replicas"))
	}
	if p.Builds.Enabled {
		if c.RegistryIP() == "" {
			errs = append(errs, fmt.Errorf("platform.builds needs an IPv4 network.serviceCIDR of /22 or larger for the registry address"))
		}
		if !p.Longhorn.Enabled {
			errs = append(errs, fmt.Errorf("platform.builds needs Longhorn for the registry's volume"))
		}
	}
	if r := p.Flux.Repository; r != nil {
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.MetalLB.Enabled {
		if _, _, err := ParseIPRange(p.MetalLB.Range); err != nil {
			errs = append(errs, fmt.Errorf("platform.metallb.range: %w", err))
		}
	}
	return errs
}

func (c *Cluster) validateStorage() []error {
	s := c.Spec.Storage
	if s.EphemeralSize == "" {
		return nil
	}
	if err := s.CheckEphemeral(); err != nil {
		return []error{fmt.Errorf("storage.ephemeralSize %q: %w", s.EphemeralSize, err)}
	}
	return nil
}

func (c *Cluster) validateAuth() []error {
	o := c.Spec.Auth.OIDC
	if o == nil {
		return nil
	}
	var errs []error
	if !strings.HasPrefix(o.Issuer, "https://") {
		errs = append(errs, fmt.Errorf("auth.oidc.issuer must be an https:// URL"))
	}
	if o.ClientID == "" {
		errs = append(errs, fmt.Errorf("auth.oidc.clientID is required"))
	}
	if o.AdminGroup != "" && o.GroupsClaim == "" {
		errs = append(errs, fmt.Errorf("auth.oidc.adminGroup needs groupsClaim"))
	}
	return errs
}

func (c *Cluster) validateTalosVersion() []error {
	contract, err := talosconfig.ParseContractFromVersion(c.Spec.TalosVersion)
	if err != nil {
		return []error{fmt.Errorf("talosVersion: %w", err)}
	}
	if !contract.UnattendedInstallConfig() || !contract.MultidocKubernetesConfigSupported() {
		return []error{fmt.Errorf("talosVersion %s: Kubit requires Talos %s or newer", c.Spec.TalosVersion, MinTalosVersion)}
	}
	return nil
}

func (c *Cluster) validateControlPlane() []error {
	var errs []error
	if len(c.Spec.Nodes) == 0 {
		errs = append(errs, errors.New("spec.nodes must not be empty"))
	}
	if len(c.ControlPlanes()) == 0 {
		errs = append(errs, errors.New("at least one controlplane node is required"))
	}
	if u, err := url.Parse(c.Spec.ControlPlane.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("controlPlane.endpoint %q must be an https URL", c.Spec.ControlPlane.Endpoint))
	}
	if v := c.Spec.ControlPlane.VIP; v != "" {
		if _, err := netip.ParseAddr(v); err != nil {
			errs = append(errs, fmt.Errorf("controlPlane.vip: %w", err))
		}
		for _, n := range c.Spec.Nodes {
			if n.IP == v || n.TargetIP() == v {
				errs = append(errs, fmt.Errorf("controlPlane.vip %s is %s's address; the VIP needs an address of its own", v, n.Hostname))
			}
		}
	}
	if u, err := url.Parse(c.Spec.ControlPlane.Endpoint); err == nil && c.Spec.ControlPlane.VIP == "" {
		for _, n := range c.ControlPlanes() {
			if target := n.TargetIP(); u.Hostname() == n.IP && target != n.IP {
				errs = append(errs, fmt.Errorf("controlPlane.endpoint points at %s's current address %s, which moves to %s; point it at the new address or a VIP", n.Hostname, n.IP, target))
			}
		}
	}
	return errs
}

func (c *Cluster) validateNetwork() []error {
	var errs []error
	for _, cidr := range []string{c.Spec.Network.PodCIDR, c.Spec.Network.ServiceCIDR} {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			errs = append(errs, fmt.Errorf("network CIDR %q: %w", cidr, err))
		}
	}
	for _, ns := range c.Spec.Network.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			errs = append(errs, fmt.Errorf("network.nameservers %q: %w", ns, err))
		}
	}
	return errs
}

func (c *Cluster) validateNodes() []error {
	var errs []error
	seenHost, seenMAC := map[string]bool{}, map[string]bool{}
	owner := map[string]string{}
	cidrs := c.clusterCIDRs()
	for i, n := range c.Spec.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if n.Role != RoleControlPlane && n.Role != RoleWorker {
			errs = append(errs, fmt.Errorf("%s.role %q must be controlplane or worker", p, n.Role))
		}
		if n.MAC != "" {
			if seenMAC[n.MAC] {
				errs = append(errs, fmt.Errorf("%s.mac %q duplicated", p, n.MAC))
			}
			seenMAC[n.MAC] = true
		}
		for k, v := range n.Taints {
			if err := validTaint(k, v); err != nil {
				errs = append(errs, fmt.Errorf("%s.taints: %w", p, err))
			}
		}
		if nn := n.Network; nn != nil {
			errs = append(errs, c.validateNodeNetwork(p, nn)...)
		}
		for _, addr := range slices.Compact([]string{n.IP, n.TargetIP()}) {
			if other, ok := owner[addr]; ok && other != n.Hostname {
				errs = append(errs, fmt.Errorf("%s: %s is also %s's address", p, addr, other))
			}
			owner[addr] = n.Hostname
			if a, err := netip.ParseAddr(addr); err == nil {
				for _, pfx := range cidrs {
					if pfx.Contains(a) {
						errs = append(errs, fmt.Errorf("%s: %s lies inside the cluster network %s", p, addr, pfx))
					}
				}
			}
		}
		if !hostnameRE.MatchString(n.Hostname) {
			errs = append(errs, fmt.Errorf("%s.hostname %q must be a DNS label", p, n.Hostname))
		}
		if seenHost[n.Hostname] {
			errs = append(errs, fmt.Errorf("%s.hostname %q duplicated", p, n.Hostname))
		}
		seenHost[n.Hostname] = true
		if _, err := netip.ParseAddr(n.IP); err != nil {
			errs = append(errs, fmt.Errorf("%s.ip: %w", p, err))
		}
		if n.MAC != "" {
			if netx.Normalize(n.MAC) == "" {
				errs = append(errs, fmt.Errorf("%s.mac %q is not a MAC address", p, n.MAC))
			}
		}
		if n.Arch != ArchAMD64 && n.Arch != ArchARM64 {
			errs = append(errs, fmt.Errorf("%s.arch %q must be amd64 or arm64", p, n.Arch))
		}
		errs = append(errs, validateNodeDisks(p, n)...)
	}
	return errs
}

func (c *Cluster) clusterCIDRs() []netip.Prefix {
	var out []netip.Prefix
	for _, cidr := range []string{c.Spec.Network.PodCIDR, c.Spec.Network.ServiceCIDR} {
		if pfx, err := netip.ParsePrefix(cidr); err == nil {
			out = append(out, pfx)
		}
	}
	return out
}

func (c *Cluster) validateNodeNetwork(p string, nn *NodeNetwork) []error {
	var errs []error
	if len(nn.Addresses) == 0 {
		errs = append(errs, fmt.Errorf("%s.network.addresses must not be empty", p))
	}
	var first netip.Prefix
	for i, a := range nn.Addresses {
		pfx, err := netip.ParsePrefix(a)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.network.addresses %q: must be CIDR notation", p, a))
			continue
		}
		if i == 0 {
			first = pfx
		}
		if pfx.Addr().Is4() && pfx.Bits() <= 30 && (pfx.Addr() == pfx.Masked().Addr() || pfx.Addr() == lastAddr(pfx)) {
			errs = append(errs, fmt.Errorf("%s.network.addresses %q is the network's own or broadcast address", p, a))
		}
		if v := c.Spec.ControlPlane.VIP; v != "" && v == pfx.Addr().String() {
			errs = append(errs, fmt.Errorf("%s.network.addresses %q collides with the control plane VIP", p, a))
		}
		if m := c.Spec.Platform.MetalLB; m.Enabled {
			if lo, hi, err := ParseIPRange(m.Range); err == nil && InRange(pfx.Addr(), lo, hi) {
				errs = append(errs, fmt.Errorf("%s.network.addresses %q lies inside the MetalLB range", p, a))
			}
		}
	}
	switch gw, err := netip.ParseAddr(nn.Gateway); {
	case nn.Gateway == "":
		errs = append(errs, fmt.Errorf("%s.network.gateway is required with a static address", p))
	case err != nil:
		errs = append(errs, fmt.Errorf("%s.network.gateway: %w", p, err))
	case first.IsValid() && !first.Masked().Contains(gw):
		errs = append(errs, fmt.Errorf("%s.network.gateway %s is outside %s", p, gw, first.Masked()))
	case first.IsValid() && first.Addr() == gw:
		errs = append(errs, fmt.Errorf("%s.network.gateway %s is the node's own address", p, gw))
	}
	for _, ns := range nn.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			errs = append(errs, fmt.Errorf("%s.network.nameservers %q: %w", p, ns, err))
		}
	}
	if len(nn.Nameservers) == 0 && len(c.Spec.Network.Nameservers) == 0 {
		errs = append(errs, fmt.Errorf("%s.network: static addresses need nameservers on the node or the cluster", p))
	}
	if nn.VLAN > 4094 {
		errs = append(errs, fmt.Errorf("%s.network.vlan %d out of range", p, nn.VLAN))
	}
	return errs
}

func validateNodeDisks(p string, n Node) []error {
	var errs []error
	if (n.InstallDisk.Path == "") == (n.InstallDisk.Selector == nil) {
		errs = append(errs, fmt.Errorf("%s.installDisk needs exactly one of path or selector", p))
	}
	if sel := n.InstallDisk.Selector; sel != nil && sel.MinSize != "" {
		if _, err := minSize(sel.MinSize); err != nil {
			errs = append(errs, fmt.Errorf("%s.installDisk.selector: %w", p, err))
		}
	}
	if len(n.DataDisks) > MaxDataDisks {
		errs = append(errs, fmt.Errorf("%s.dataDisks: at most %d", p, MaxDataDisks))
	}
	seenDisk := map[string]bool{}
	for _, d := range n.DataDisks {
		switch {
		case d == "":
			errs = append(errs, fmt.Errorf("%s.dataDisks: empty path", p))
		case d == n.InstallDisk.Path:
			errs = append(errs, fmt.Errorf("%s.dataDisks: %s is the install disk", p, d))
		case seenDisk[d]:
			errs = append(errs, fmt.Errorf("%s.dataDisks: %s listed twice", p, d))
		}
		seenDisk[d] = true
	}
	return errs
}

func validTaint(key, value string) error {
	effect := value
	if i := strings.LastIndexByte(value, ':'); i >= 0 {
		effect = value[i+1:]
	}
	switch effect {
	case "NoSchedule", "PreferNoSchedule", "NoExecute":
		return nil
	}
	return fmt.Errorf("%s=%q: effect must be NoSchedule, PreferNoSchedule or NoExecute (write value:Effect)", key, value)
}

func lastAddr(pfx netip.Prefix) netip.Addr {
	b := pfx.Masked().Addr().As4()
	host := uint32(1)<<(32-pfx.Bits()) - 1
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) | host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
