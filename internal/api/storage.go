package api

import (
	"net/http"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/store"
)

type storageView struct {
	*k8s.Storage
	Longhorn *longhornView `json:"longhorn,omitempty"`
}

type longhornView struct {
	Replicas         int            `json:"replicas"`
	Auto             int            `json:"auto"`
	Max              int            `json:"max"`
	Applied          int            `json:"applied"`
	UsableBytes      uint64         `json:"usableBytes"`
	UsableByReplicas []uint64       `json:"usableByReplicas"`
	LeftBytes        uint64         `json:"leftBytes"`
	ProvisionedBytes uint64         `json:"provisionedBytes"`
	WrittenBytes     uint64         `json:"writtenBytes"`
	Volumes          map[string]int `json:"volumes"`
	Live             bool           `json:"live"`
	Error            string         `json:"error,omitempty"`
	Nodes            []storageNode  `json:"nodes"`
}

type storageNode struct {
	Hostname   string            `json:"hostname"`
	Role       config.Role       `json:"role"`
	Disks      []string          `json:"disks"`
	SystemDisk bool              `json:"systemDisk"`
	Awaiting   bool              `json:"awaiting"`
	Editable   bool              `json:"editable"`
	Live       *k8s.LonghornNode `json:"live,omitempty"`
}

func (s *Server) storageRoutes() {
	s.mux.HandleFunc("GET /api/v1/clusters/{name}/storage", s.handleStorage)
	s.mux.HandleFunc("GET /api/v1/clusters/{name}/storage/longhorn", s.handleLonghorn)
	s.mux.HandleFunc("PUT /api/v1/clusters/{name}/nodes/{hostname}/storage", s.handleNodeStorage)
}

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	kc, err := s.manager.KubeClient(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	st, err := kc.Storage(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	view := storageView{Storage: st, Longhorn: s.longhornViewFor(name, st.Longhorn)}
	if view.Longhorn != nil {
		view.Longhorn.Error = st.LonghornError
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleLonghorn(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	kc, err := s.manager.KubeClient(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}
	lh, err := kc.Longhorn(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.longhornViewFor(name, lh))
}

func (s *Server) longhornViewFor(name string, lh *k8s.Longhorn) *longhornView {
	d, err := s.manager.Desired(name)
	if err != nil || !d.Cluster.Spec.Platform.Longhorn.Enabled && lh == nil {
		return nil
	}
	return s.longhornView(d.Cluster, lh)
}

func (s *Server) longhornView(c *config.Cluster, lh *k8s.Longhorn) *longhornView {
	v := &longhornView{Replicas: c.Spec.Platform.Longhorn.Replicas, Auto: c.AutoReplicas(), Max: len(c.LonghornNodes()), Live: lh != nil, Volumes: map[string]int{}, Nodes: []storageNode{}}
	var live []k8s.LonghornNode
	var volumes []k8s.LonghornVolume
	if lh != nil {
		live, volumes, v.Applied = lh.Nodes, lh.Volumes, lh.Replicas
	}
	byName := map[string]*k8s.LonghornNode{}
	var capacities, headroom []uint64
	for i, l := range live {
		byName[l.Name] = &live[i]
		if l.Capacity > 0 {
			capacities = append(capacities, uint64(l.Capacity))
			headroom = append(headroom, uint64(l.Headroom))
		}
	}
	v.UsableBytes, v.LeftBytes = config.UsableBytes(capacities, v.Applied), config.UsableBytes(headroom, v.Applied)
	v.UsableByReplicas = config.UsableByReplicas(capacities, max(v.Max, len(capacities)))
	for _, vol := range volumes {
		v.ProvisionedBytes += uint64(max(vol.Size, 0))
		v.WrittenBytes += uint64(max(vol.Actual, 0))
		health := vol.Robustness
		if vol.State == "detached" {
			health = "detached"
		}
		v.Volumes[health]++
	}
	for _, n := range c.Spec.Nodes {
		if !c.RunsWorkloads(n) {
			continue
		}
		row := storageNode{Hostname: n.Hostname, Role: n.Role, Disks: c.StorageMounts(n), SystemDisk: c.UsesSystemDisk(n), Awaiting: s.awaitsInstall(n), Live: byName[n.Hostname]}
		row.Editable = row.Awaiting && len(n.DataDisks) == 0
		if row.Live != nil {
			row.Disks = row.Live.Disks
		}
		if row.Disks == nil {
			row.Disks = []string{}
		}
		v.Nodes = append(v.Nodes, row)
	}
	return v
}

func (s *Server) awaitsInstall(n config.Node) bool {
	m, err := s.store.GetMachine(n.MAC)
	return n.MAC != "" && err == nil && m.Kind() == store.KindMaintenance
}

func (s *Server) handleNodeStorage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SystemDisk bool   `json:"systemDisk"`
		Hash       string `json:"hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	ns, err := s.nodeOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !s.awaitsInstall(ns.n) {
		writeErr(w, conflict(ns.n.Hostname+" is installed; its disk layout is fixed."))
		return
	}
	if err := repo.SetNodeSystemDisk(ns.dir, req.Hash, ns.n.Hostname, req.SystemDisk); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, ns.dir)
}
