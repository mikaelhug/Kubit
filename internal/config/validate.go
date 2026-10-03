package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
)

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (c *Cluster) Validate() error {
	var errs []error
	for _, section := range []func() []error{
		c.validateHeader, c.validateBackup, c.validatePlatform, c.validateStorage, c.validateAuth,
		c.validateTalosVersion, c.validateControlPlane, c.validateNetwork, c.validatePools, c.validateNodes, c.validatePatches,
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
	if iv := c.Spec.Backup.Etcd.Interval; iv != "" && iv != "0" {
		if d, err := time.ParseDuration(iv); err != nil || d < 5*time.Minute {
			errs = append(errs, fmt.Errorf("backup.etcd.interval %q: a Go duration of at least 5m, or 0 to disable", iv))
		}
	}
	if c.Spec.Backup.Etcd.Keep < 1 {
		errs = append(errs, fmt.Errorf("backup.etcd.keep must be at least 1"))
	}
	if err := c.Spec.Maintenance.Validate(); err != nil {
		errs = append(errs, err)
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
	var errs []error
	switch s.Encryption {
	case "", EncryptionTPM, EncryptionNodeID:
	default:
		errs = append(errs, fmt.Errorf("storage.encryption %q must be tpm or nodeID", s.Encryption))
	}
	if s.Encryption == EncryptionTPM {
		for _, n := range c.Spec.Nodes {
			if !n.TPM {
				errs = append(errs, fmt.Errorf("%s: no TPM; storage.encryption is tpm", n.Hostname))
			}
		}
	}
	if s.EphemeralSize == "" {
		return errs
	}
	b, err := s.EphemeralBytes()
	if err != nil {
		return append(errs, fmt.Errorf("storage.ephemeralSize %q: %w", s.EphemeralSize, err))
	}
	if b < MinEphemeralBytes {
		return append(errs, fmt.Errorf("storage.ephemeralSize %q: at least 10GiB", s.EphemeralSize))
	}
	return errs
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

func (c *Cluster) validatePools() []error {
	var errs []error
	poolNames := map[string]bool{}
	cpPools := 0
	for i, pl := range c.Spec.Pools {
		pp := fmt.Sprintf("pools[%d]", i)
		if !hostnameRE.MatchString(pl.Name) {
			errs = append(errs, fmt.Errorf("%s.name %q must be a DNS label", pp, pl.Name))
		}
		if poolNames[pl.Name] {
			errs = append(errs, fmt.Errorf("%s.name %q duplicated", pp, pl.Name))
		}
		poolNames[pl.Name] = true
		if pl.Role != RoleControlPlane && pl.Role != RoleWorker {
			errs = append(errs, fmt.Errorf("%s.role %q must be controlplane or worker", pp, pl.Role))
		}
		if pl.Role == RoleControlPlane {
			cpPools++
		}
		for k, v := range pl.Taints {
			if err := validTaint(k, v); err != nil {
				errs = append(errs, fmt.Errorf("%s.taints: %w", pp, err))
			}
		}
	}
	if cpPools != 1 {
		errs = append(errs, fmt.Errorf("exactly one pool must have role controlplane, found %d", cpPools))
	}
	return errs
}

func (c *Cluster) validateNodes() []error {
	var errs []error
	var staticAddrs []netip.Prefix
	seenHost, seenIP, seenMAC := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, n := range c.Spec.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if c.poolByName(n.Pool) == nil {
			errs = append(errs, fmt.Errorf("%s.pool %q is not declared in spec.pools", p, n.Pool))
		}
		if n.MAC != "" {
			if seenMAC[strings.ToLower(n.MAC)] {
				errs = append(errs, fmt.Errorf("%s.mac %q duplicated", p, n.MAC))
			}
			seenMAC[strings.ToLower(n.MAC)] = true
		}
		for k, v := range n.Taints {
			if err := validTaint(k, v); err != nil {
				errs = append(errs, fmt.Errorf("%s.taints: %w", p, err))
			}
		}
		if nn := n.Network; nn != nil {
			errs = append(errs, c.validateNodeNetwork(p, nn, &staticAddrs)...)
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
			if _, err := net.ParseMAC(n.MAC); err != nil {
				errs = append(errs, fmt.Errorf("%s.mac: %w", p, err))
			}
		}
		if seenIP[n.IP] {
			errs = append(errs, fmt.Errorf("%s.ip %q duplicated", p, n.IP))
		}
		seenIP[n.IP] = true
		if n.Arch != ArchAMD64 && n.Arch != ArchARM64 {
			errs = append(errs, fmt.Errorf("%s.arch %q must be amd64 or arm64", p, n.Arch))
		}
		errs = append(errs, validateNodeDisks(p, n)...)
	}
	return errs
}

func (c *Cluster) validateNodeNetwork(p string, nn *NodeNetwork, staticAddrs *[]netip.Prefix) []error {
	var errs []error
	if len(nn.Addresses) == 0 {
		errs = append(errs, fmt.Errorf("%s.network.addresses must not be empty", p))
	}
	for _, a := range nn.Addresses {
		pfx, err := netip.ParsePrefix(a)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.network.addresses %q: must be CIDR notation", p, a))
			continue
		}
		for _, other := range *staticAddrs {
			if other.Addr() == pfx.Addr() {
				errs = append(errs, fmt.Errorf("%s.network.addresses %q used by another node", p, a))
			}
		}
		*staticAddrs = append(*staticAddrs, pfx)
		if v := c.Spec.ControlPlane.VIP; v != "" && v == pfx.Addr().String() {
			errs = append(errs, fmt.Errorf("%s.network.addresses %q collides with the control plane VIP", p, a))
		}
		if m := c.Spec.Platform.MetalLB; m.Enabled {
			if lo, hi, err := ParseIPRange(m.Range); err == nil && inRange(pfx.Addr(), lo, hi) {
				errs = append(errs, fmt.Errorf("%s.network.addresses %q lies inside the MetalLB range", p, a))
			}
		}
	}
	if nn.Gateway != "" {
		if _, err := netip.ParseAddr(nn.Gateway); err != nil {
			errs = append(errs, fmt.Errorf("%s.network.gateway: %w", p, err))
		}
	}
	for _, ns := range nn.Nameservers {
		if _, err := netip.ParseAddr(ns); err != nil {
			errs = append(errs, fmt.Errorf("%s.network.nameservers %q: %w", p, ns, err))
		}
	}
	if len(nn.Nameservers) == 0 && len(c.Spec.Network.Nameservers) == 0 {
		errs = append(errs, fmt.Errorf("%s uses static addressing but no nameservers are set (on the node or the cluster) — the node would have no DNS and image pulls would fail; add network.nameservers", p))
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
