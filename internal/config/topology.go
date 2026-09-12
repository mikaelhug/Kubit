package config

// Topology is the recommended control plane / worker split for a node count.
type Topology struct {
	ControlPlanes   int
	Workers         int
	AllowScheduling bool
	HA              bool
}

// Recommend follows the spec: 1–2 nodes → single control plane; 3–5 → three schedulable
// control planes; 6+ → three dedicated control planes. Two control planes are never
// recommended: etcd with two members has no fault tolerance.
func Recommend(n int) Topology {
	switch {
	case n <= 0:
		return Topology{}
	case n < 3:
		return Topology{ControlPlanes: 1, Workers: n - 1, AllowScheduling: true}
	case n < 6:
		return Topology{ControlPlanes: 3, Workers: n - 3, AllowScheduling: true, HA: true}
	default:
		return Topology{ControlPlanes: 3, Workers: n - 3, HA: true}
	}
}
