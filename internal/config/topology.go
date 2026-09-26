package config

type Topology struct {
	ControlPlanes   int
	Workers         int
	AllowScheduling bool
	HA              bool
}

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
