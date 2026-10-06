package store

import (
	"bytes"
	"reflect"
	"sort"

	"github.com/mikael/kubit/internal/config"
)

type ClusterRow struct {
	Name  string          `json:"name"`
	Spec  *config.Cluster `json:"spec"`
	State string          `json:"state"`
	Hash  string          `json:"hash"`
}

type ClusterSecrets struct {
	Talosconfig []byte
	Kubeconfig  []byte
}

func (s *Store) PutCluster(c ClusterRow) {
	s.mu.Lock()
	prev, had := s.clusters[c.Name]
	if c.State == "" {
		c.State = prev.State
	}
	c.Spec = c.Spec.Clone()
	s.clusters[c.Name] = c
	s.mu.Unlock()
	if had && prev.State == c.State && reflect.DeepEqual(prev.Spec, c.Spec) {
		return
	}
	s.notify(Change{Table: "clusters", Cluster: c.Name, Key: c.Name})
}

func (s *Store) GetCluster(name string) (*ClusterRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clusters[name]
	if !ok {
		return nil, notFound("cluster %q", name)
	}
	c.Spec = c.Spec.Clone()
	c.Hash = s.hashes[name]
	return &c, nil
}

func (s *Store) ListClusters() []ClusterRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ClusterRow, 0, len(s.clusters))
	for _, c := range s.clusters {
		c.Hash = s.hashes[c.Name]
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) ClusterVIPs() map[string]string {
	out := map[string]string{}
	for _, r := range s.ListClusters() {
		if vip := r.Spec.Spec.ControlPlane.VIP; vip != "" {
			out[vip] = r.Name
		}
	}
	return out
}

func (s *Store) SetClusterHash(name, hash string) {
	s.mu.Lock()
	changed := s.hashes[name] != hash
	s.hashes[name] = hash
	_, known := s.clusters[name]
	s.mu.Unlock()
	if changed && known {
		s.notify(Change{Table: "clusters", Cluster: name, Key: name})
	}
}

func (s *Store) SetClusterState(name, state string) {
	s.mu.Lock()
	c, ok := s.clusters[name]
	if ok {
		c.State = state
		s.clusters[name] = c
	}
	s.mu.Unlock()
	if ok {
		s.notify(Change{Table: "clusters", Cluster: name, Key: name})
	}
}

func (s *Store) PutClusterSecrets(name string, sec ClusterSecrets) {
	s.mu.Lock()
	prev, had := s.secrets[name]
	s.secrets[name] = ClusterSecrets{Talosconfig: bytes.Clone(sec.Talosconfig), Kubeconfig: bytes.Clone(sec.Kubeconfig)}
	s.mu.Unlock()
	if !had || !bytes.Equal(prev.Talosconfig, sec.Talosconfig) || !bytes.Equal(prev.Kubeconfig, sec.Kubeconfig) {
		s.notify(Change{Table: "secrets", Cluster: name, Key: name})
	}
}

func (s *Store) GetClusterSecrets(name string) (*ClusterSecrets, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec, ok := s.secrets[name]
	if !ok {
		return nil, notFound("secrets for cluster %q", name)
	}
	return &sec, nil
}
