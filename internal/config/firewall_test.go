package config_test

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
)

func withFirewall(t *testing.T, yaml string) (*config.Cluster, *config.Generated) {
	t.Helper()
	c, err := config.Parse([]byte(strings.Replace(yaml, "spec:\n", "spec:\n  network: { firewall: true }\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	g, err := config.Generate(c, sharedSecrets(t), config.FixedInstaller(installer))
	if err != nil {
		t.Fatal(err)
	}
	return c, g
}

func rules(cfg talosconfig.Provider) map[string]*network.RuleConfigV1Alpha1 {
	out := map[string]*network.RuleConfigV1Alpha1{}
	for _, d := range cfg.Documents() {
		if r, ok := d.(*network.RuleConfigV1Alpha1); ok {
			out[r.MetaName] = r
		}
	}
	return out
}

func ruleText(r *network.RuleConfigV1Alpha1) string {
	var ports, from []string
	for _, p := range r.PortSelector.Ports {
		ports = append(ports, fmt.Sprintf("%d-%d", p.Lo, p.Hi))
	}
	for _, s := range r.Subnets() {
		from = append(from, s.String())
	}
	return fmt.Sprintf("%s %s from %s", r.PortSelector.Protocol, strings.Join(ports, ","), strings.Join(from, ","))
}

func TestClusterSubnets(t *testing.T) {
	c, err := config.Parse([]byte(strings.Replace(sampleCluster,
		"worker-01, ip: 192.168.64.5,",
		"worker-01, ip: 10.1.2.5, network: { addresses: [10.1.2.5/23, 172.16.0.9/16], nameservers: [1.1.1.1] },", 1)))
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Nodes = append(c.Spec.Nodes, config.Node{Hostname: "extra", IP: "192.168.64.77"})
	got := config.ClusterSubnets(c)
	want := []netip.Prefix{netip.MustParsePrefix("10.1.2.0/23"), netip.MustParsePrefix("172.16.0.0/16"), netip.MustParsePrefix("192.168.64.0/24")}
	if !slices.Equal(got, want) {
		t.Errorf("subnets = %v, want %v", got, want)
	}
}

func TestFirewallRules(t *testing.T) {
	c, g := withFirewall(t, sampleCluster)
	lan := "192.168.64.0/24"
	cp := load(t, g.Nodes["cp-01"])
	if a := doc[*network.DefaultActionConfigV1Alpha1](t, cp); a.Ingress != nethelpers.DefaultActionBlock {
		t.Errorf("default ingress = %s", a.Ingress)
	}
	wantCP := map[string]string{
		"apid":           "tcp 50000-50000 from 0.0.0.0/0,::/0",
		"kubernetes-api": "tcp 6443-6443 from 0.0.0.0/0,::/0",
		"kubelet":        "tcp 10250-10250 from " + lan + "," + c.Spec.Network.PodCIDR,
		"trustd":         "tcp 50001-50001 from " + lan,
		"etcd":           "tcp 2379-2380,2383-2383 from " + lan,
		"flannel-vxlan":  "udp 4789-4789 from " + lan,
		"metallb-tcp":    "tcp 7946-7946 from " + lan,
		"metallb-udp":    "udp 7946-7946 from " + lan,
	}
	assertRules(t, "cp-01", rules(cp), wantCP)

	worker := load(t, g.Nodes["worker-01"])
	wantWorker := map[string]string{}
	for _, name := range []string{"apid", "kubelet", "flannel-vxlan", "metallb-tcp", "metallb-udp"} {
		wantWorker[name] = wantCP[name]
	}
	assertRules(t, "worker-01", rules(worker), wantWorker)

	_, noLB := withFirewall(t, strings.Replace(sampleCluster, "metallb: { enabled: true, range: 192.168.64.200-192.168.64.220 }", "metallb: { enabled: false }", 1))
	for host, b := range noLB.Nodes {
		r := rules(load(t, b))
		if r["metallb-tcp"] != nil || r["metallb-udp"] != nil {
			t.Errorf("%s: MetalLB rules without MetalLB", host)
		}
		if r["ingress-nodeports"] == nil || ruleText(r["ingress-nodeports"]) != "tcp 30000-32767 from 0.0.0.0/0,::/0" {
			t.Errorf("%s: ingress on NodePorts needs its ports open: %v", host, r["ingress-nodeports"])
		}
	}

	again, err := config.Generate(c, sharedSecrets(t), config.FixedInstaller(installer))
	if err != nil {
		t.Fatal(err)
	}
	for host, b := range g.Nodes {
		if !bytes.Equal(b, again.Nodes[host]) {
			t.Errorf("%s: firewall output is not deterministic", host)
		}
	}
}

func assertRules(t *testing.T, host string, got map[string]*network.RuleConfigV1Alpha1, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		slices.Sort(names)
		t.Errorf("%s: rules %v, want %d", host, names, len(want))
	}
	for name, w := range want {
		r := got[name]
		if r == nil {
			t.Errorf("%s: rule %s missing", host, name)
			continue
		}
		if s := ruleText(r); s != w {
			t.Errorf("%s: %s = %q, want %q", host, name, s, w)
		}
	}
}

func TestFirewallOffEmitsNothing(t *testing.T) {
	for _, decl := range []string{sampleCluster, strings.Replace(sampleCluster, "spec:\n", "spec:\n  network: { firewall: false }\n", 1)} {
		c, err := config.Parse([]byte(decl))
		if err != nil {
			t.Fatal(err)
		}
		g, err := config.Generate(c, sharedSecrets(t), config.FixedInstaller(installer))
		if err != nil {
			t.Fatal(err)
		}
		for host, b := range g.Nodes {
			cfg := load(t, b)
			if hasDoc[*network.DefaultActionConfigV1Alpha1](cfg) || len(rules(cfg)) > 0 {
				t.Errorf("%s: firewall documents with the firewall off", host)
			}
		}
	}
}

func TestDesignTurnsFirewallOn(t *testing.T) {
	c, warnings := config.Design("lab", machines(), config.DesignOptions{})
	if !c.FirewallOn() {
		t.Error("a designed cluster has the firewall on")
	}
	if slices.ContainsFunc(warnings, func(w config.Warning) bool { return w.Code == "firewall-off" }) {
		t.Error("firewall-off reported for a designed cluster")
	}
	parsed, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.FirewallOn() {
		t.Error("an existing cluster without the key keeps the firewall off")
	}
	if !slices.ContainsFunc(config.Lint(parsed, nil), func(w config.Warning) bool { return w.Code == "firewall-off" && w.Level == "info" }) {
		t.Error("firewall-off finding missing")
	}
}
