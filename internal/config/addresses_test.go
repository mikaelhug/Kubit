package config_test

import (
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
)

const twoNodes = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  controlPlane: { endpoint: "https://192.168.5.240:6443" }
  network: { nameservers: [192.168.5.1] }
  nodes:
    - { hostname: cp-01, ip: 192.168.5.240, role: controlplane, installDisk: { path: /dev/nvme0n1 }CP }
    - { hostname: w-01, ip: 192.168.5.204, role: worker, installDisk: { path: /dev/nvme0n1 }W }
`

func spec(cp, w string) string {
	return strings.NewReplacer("CP", cp, "W", w).Replace(twoNodes)
}

func TestConflictingAddressOrStaleEndpointIsRefused(t *testing.T) {
	for _, c := range []struct{ name, cp, w, want string }{
		{"target taken by another node", "", ", network: { addresses: [192.168.5.240/24], gateway: 192.168.5.1 }", "also cp-01's address"},
		{"inside the pod network", "", ", network: { addresses: [10.244.0.9/16], gateway: 10.244.0.1 }", "inside the cluster network"},
		{"endpoint must follow", ", network: { addresses: [192.168.5.51/24], gateway: 192.168.5.1 }", "", "point it at the new address"},
	} {
		_, err := config.Parse([]byte(spec(c.cp, c.w)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
	moved := strings.Replace(spec(", network: { addresses: [192.168.5.51/24], gateway: 192.168.5.1 }", ""), "https://192.168.5.240:6443", "https://192.168.5.51:6443", 1)
	c, err := config.Parse([]byte(moved))
	if err != nil {
		t.Fatalf("an endpoint that follows the move is valid: %v", err)
	}
	if n, ok := c.EndpointNode(); !ok || n.Hostname != "cp-01" {
		t.Errorf("the endpoint node is found by its new address: %v %v", n.Hostname, ok)
	}
}

func TestLiveAddressingReadsBackAsDeclaredSoNothingMoves(t *testing.T) {
	for name, cp := range map[string]string{
		"dhcp":   "",
		"static": ", network: { addresses: [192.168.5.240/24], gateway: 192.168.5.1 }",
		"vlan":   ", network: { addresses: [192.168.5.240/24], gateway: 192.168.5.1, vlan: 20, mtu: 9000 }",
	} {
		c, err := config.Parse([]byte(spec(cp, "")))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		g, err := config.Generate(c, sharedSecrets(t), "installer")
		if err != nil {
			t.Fatal(err)
		}
		live, err := config.ReadLive(g.Nodes["cp-01"])
		if err != nil || !live.Found {
			t.Fatalf("%s: %+v %v", name, live, err)
		}
		if want := c.Spec.Nodes[0].Addressing(); !live.Addressing.Equal(want) {
			t.Errorf("%s: live %+v, declared %+v", name, live.Addressing, want)
		}
		if live.Endpoint != "https://192.168.5.240:6443" {
			t.Errorf("%s: endpoint %q", name, live.Endpoint)
		}
	}
}
