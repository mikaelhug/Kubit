package cluster

import (
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
