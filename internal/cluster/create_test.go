package cluster

import (
	"testing"

	"github.com/mikael/kubit/internal/config"
)

func node(host, mac, ip string) config.Node {
	return config.Node{Hostname: host, MAC: mac, IP: ip, Role: config.RoleControlPlane}
}

func cl(nodes ...config.Node) *config.Cluster {
	c := &config.Cluster{}
	c.Spec.Nodes = nodes
	return c
}

func TestSameNodesIgnoresIP(t *testing.T) {
	// Same MAC + role, IP changed (install rewrite or a new DHCP lease): same node set,
	// so a retry is not rejected as "a different node set".
	if !sameNodes(cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.5")), cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.9"))) {
		t.Error("a changed IP on the same MAC must still be the same node set")
	}
	// Different MAC is genuinely a different machine.
	if sameNodes(cl(node("cp-01", "52:54:00:00:00:01", "10.0.0.5")), cl(node("cp-01", "52:54:00:00:00:02", "10.0.0.5"))) {
		t.Error("a different MAC must be a different node set")
	}
	// Different count.
	if sameNodes(cl(node("a", "52:54:00:00:00:01", "10.0.0.5")), cl(node("a", "52:54:00:00:00:01", "10.0.0.5"), node("b", "52:54:00:00:00:02", "10.0.0.6"))) {
		t.Error("different node counts are different sets")
	}
}
