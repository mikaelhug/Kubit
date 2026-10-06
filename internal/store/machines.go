package store

import (
	"slices"
	"sort"
	"time"

	"github.com/mikael/kubit/internal/netx"
)

const (
	stateMaintenance = "maintenance"
	stateConfigured  = "configured"
)

type Machine struct {
	MAC          string   `json:"mac"`
	UUID         string   `json:"uuid,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	IP           string   `json:"ip"`
	IPsSeen      []string `json:"ipsSeen,omitempty"`
	Cluster      string   `json:"cluster"`
	Hostname     string   `json:"hostname"`
	Role         string   `json:"role"`
	Arch         string   `json:"arch"`
	State        string   `json:"state"`
	Hardware     []byte   `json:"-"`
	TalosVersion string   `json:"talosVersion"`
	LastSeen     string   `json:"lastSeen"`
}

type Kind string

const (
	KindMember      Kind = "member"
	KindMaintenance Kind = "maintenance"
	KindConfigured  Kind = "configured"
	KindOffline     Kind = "offline"
)

func (m *Machine) Kind() Kind {
	switch {
	case m.Cluster != "":
		return KindMember
	case m.State == stateMaintenance:
		return KindMaintenance
	case m.State == stateConfigured:
		return KindConfigured
	default:
		return KindOffline
	}
}

func (m *Machine) Talos() bool {
	k := m.Kind()
	return k == KindMember || k == KindMaintenance
}

func (m *Machine) unassign(state string) {
	m.Cluster, m.Hostname, m.Role, m.State = "", "", "", state
}

func MachineKey(mac, ip string) string {
	if mac != "" {
		return netx.MACKey(mac)
	}
	return "ip:" + ip
}

const lastSeenEvery = 5 * time.Second

func (s *Store) UpsertNode(n Machine) {
	s.mu.Lock()
	key := MachineKey(n.MAC, n.IP)
	if n.MAC == "" && n.IP != "" {
		if k, ok := s.keyByIP(n.IP); ok {
			key = k
		}
	}
	var changed []string
	removed := false
	prev, had := s.machines[key]
	if ghost, ok := s.machines["ip:"+n.IP]; ok && n.MAC != "" && n.IP != "" {
		delete(s.machines, "ip:"+n.IP)
		removed = true
		if !had {
			prev, had = ghost, true
		}
	}
	if n.IP != "" {
		for k, m := range s.machines {
			if k != key && m.IP == n.IP {
				m.IP = ""
				s.machines[k] = m
				changed = append(changed, k)
			}
		}
	}
	next := merge(prev, n, had)
	next.MAC = key
	next.LastSeen = time.Now().UTC().Format(time.RFC3339Nano)
	s.machines[key] = next
	s.mu.Unlock()
	if !had || !same(prev, next) || !seenWithin(prev.LastSeen, lastSeenEvery) {
		changed = append(changed, key)
	}
	for _, k := range changed {
		s.notify(Change{Table: "machines", Key: k})
	}
	if removed {
		s.notify(Change{Table: "machines"})
	}
}

func (s *Store) Touch(key string) {
	s.mu.Lock()
	m, ok := s.machines[key]
	stale := ok && !seenWithin(m.LastSeen, lastSeenEvery)
	if ok {
		m.LastSeen = time.Now().UTC().Format(time.RFC3339Nano)
		s.machines[key] = m
	}
	s.mu.Unlock()
	if stale {
		s.notify(Change{Table: "machines", Key: key})
	}
}

func merge(prev, n Machine, had bool) Machine {
	if !had {
		if n.IPsSeen == nil {
			n.IPsSeen = []string{}
		}
		return n
	}
	out := prev
	out.IPsSeen = slices.Clone(prev.IPsSeen)
	if prev.IP != "" && n.IP != "" && prev.IP != n.IP && !slices.Contains(out.IPsSeen, prev.IP) {
		out.IPsSeen = append(out.IPsSeen, prev.IP)
	}
	pick := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	pick(&out.UUID, n.UUID)
	pick(&out.Serial, n.Serial)
	pick(&out.IP, n.IP)
	pick(&out.Arch, n.Arch)
	pick(&out.State, n.State)
	pick(&out.TalosVersion, n.TalosVersion)
	if len(n.Hardware) > 0 {
		out.Hardware = n.Hardware
	}
	if n.State == stateMaintenance && n.Cluster == "" {
		out.Cluster, out.Hostname, out.Role = "", "", ""
		return out
	}
	pick(&out.Cluster, n.Cluster)
	pick(&out.Hostname, n.Hostname)
	pick(&out.Role, n.Role)
	return out
}

func same(a, b Machine) bool {
	return a.UUID == b.UUID && a.Serial == b.Serial && a.IP == b.IP && slices.Equal(a.IPsSeen, b.IPsSeen) && a.Cluster == b.Cluster && a.Hostname == b.Hostname &&
		a.Role == b.Role && a.Arch == b.Arch && a.State == b.State && string(a.Hardware) == string(b.Hardware) && a.TalosVersion == b.TalosVersion
}

func seenWithin(ts string, d time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, ts)
	return err == nil && time.Since(t) < d
}

func (s *Store) keyByIP(ip string) (string, bool) {
	if _, ok := s.machines["ip:"+ip]; ok {
		return "ip:" + ip, true
	}
	for k, m := range s.machines {
		if m.IP == ip {
			return k, true
		}
	}
	return "", false
}

func (s *Store) GetMachine(mac string) (*Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.machines[netx.MACKey(mac)]
	if !ok {
		return nil, notFound("machine %s", mac)
	}
	return &m, nil
}

func (s *Store) GetNode(ip string) (*Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keyByIP(ip)
	if !ok {
		return nil, notFound("machine at %s", ip)
	}
	m := s.machines[k]
	return &m, nil
}

func (s *Store) ListNodes(cluster string) []Machine {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Machine{}
	for _, m := range s.machines {
		if cluster == "" || m.Cluster == cluster {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.IP < b.IP
	})
	return out
}

func (s *Store) SetNodeState(ip, state string) {
	s.updateAt(ip, func(m *Machine) { m.State = state })
}

func (s *Store) UnassignMachine(mac, ip, state string) {
	s.mu.Lock()
	key := netx.MACKey(mac)
	m, ok := s.machines[key]
	if mac != "" && ok {
		m.unassign(state)
		s.machines[key] = m
	}
	s.mu.Unlock()
	if mac != "" && ok {
		s.notify(Change{Table: "machines", Key: key})
		return
	}
	s.updateAt(ip, func(m *Machine) { m.unassign(state) })
}

func (s *Store) updateAt(ip string, fn func(*Machine)) {
	s.mu.Lock()
	k, ok := s.keyByIP(ip)
	if ok {
		m := s.machines[k]
		fn(&m)
		s.machines[k] = m
	}
	s.mu.Unlock()
	if ok {
		s.notify(Change{Table: "machines", Key: k})
	}
}
