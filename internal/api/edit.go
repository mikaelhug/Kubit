package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/netx"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/store"
)

func (s *Server) editRoutes() {
	r := s.mux
	r.HandleFunc("POST /api/v1/design", s.handleDesign)
	r.HandleFunc("POST /api/v1/design/checks", s.handleDesign)
	r.HandleFunc("GET /api/v1/clusters/{name}/repo", s.handleRepo)
	r.HandleFunc("POST /api/v1/repos", s.handleCreateRepo)
	r.HandleFunc("GET /api/v1/dirs", s.handleDirs)
	r.HandleFunc("POST /api/v1/clusters/{name}/nodes", s.handleAddNodes)
	r.HandleFunc("DELETE /api/v1/clusters/{name}/nodes/{hostname}", s.handleRemoveNode)
	r.HandleFunc("GET /api/v1/clusters/{name}/nodes/{hostname}/network", s.handleNodeNetwork)
	r.HandleFunc("PUT /api/v1/clusters/{name}/nodes/{hostname}/network", s.handleNodeNetworkPut)
	r.HandleFunc("POST /api/v1/clusters/{name}/apply", s.handleApply)
	r.HandleFunc("GET /api/v1/clusters/{name}/apply", s.handleApplyRun)
	r.HandleFunc("POST /api/v1/clusters/{name}/destroy", s.handleDestroy)
}

func (s *Server) machineOf(mac string) (config.Machine, error) {
	row, err := s.store.GetMachine(mac)
	if err != nil {
		return config.Machine{}, err
	}
	if row.Kind() != store.KindMaintenance {
		return config.Machine{}, conflict("Only a machine in maintenance mode can be added.")
	}
	inv, ok := inventoryOf(row)
	if !ok {
		return config.Machine{}, conflict("No hardware inventory for this machine yet; scan it again.")
	}
	return repo.Machine(row.IP, &inv), nil
}

type machineChoice struct {
	MAC         string      `json:"mac"`
	Role        config.Role `json:"role,omitempty"`
	Address     string      `json:"address,omitempty"`
	Gateway     string      `json:"gateway,omitempty"`
	Nameservers []string    `json:"nameservers,omitempty"`
}

type liveNet struct {
	Address     string   `json:"address,omitempty"`
	Gateway     string   `json:"gateway,omitempty"`
	Nameservers []string `json:"nameservers,omitempty"`
}

type chosen struct {
	machines []config.Machine
	roles    map[string]config.Role
	networks map[string]*config.NodeNetwork
	live     map[string]liveNet
}

func cidrOf(address, live string) string {
	address = strings.TrimSpace(address)
	if address == "" || strings.Contains(address, "/") {
		return address
	}
	if _, bits, ok := strings.Cut(live, "/"); ok {
		return address + "/" + bits
	}
	return address + "/24"
}

type designRequest struct {
	Cluster  string          `json:"cluster,omitempty"`
	Dir      string          `json:"dir,omitempty"`
	Name     string          `json:"name,omitempty"`
	VIP      string          `json:"vip,omitempty"`
	Machines []machineChoice `json:"machines"`
	Hash     string          `json:"hash,omitempty"`
	Apps     *appsRequest    `json:"apps,omitempty"`
}

type designNode struct {
	Hostname  string              `json:"hostname"`
	IP        string              `json:"ip"`
	MAC       string              `json:"mac"`
	Role      config.Role         `json:"role"`
	Disk      string              `json:"disk"`
	DiskBytes uint64              `json:"diskBytes,omitempty"`
	Network   *config.NodeNetwork `json:"network,omitempty"`
	Live      liveNet             `json:"live"`
	InUse     bool                `json:"inUse,omitempty"`
}

type designView struct {
	Cluster  string       `json:"cluster"`
	Dir      string       `json:"dir"`
	New      bool         `json:"new"`
	Nodes    []designNode `json:"nodes"`
	VIP      string       `json:"vip,omitempty"`
	VIPInUse bool         `json:"vipInUse,omitempty"`
	Endpoint string       `json:"endpoint"`
	Talos    string       `json:"talosVersion"`
	K8s      string       `json:"kubernetesVersion"`
	Warnings []string     `json:"warnings"`
	Hash     string       `json:"hash,omitempty"`
	Apps     *appsReview  `json:"apps,omitempty"`
}

type design struct {
	view  designView
	spec  *config.Cluster
	added []config.Node
	apps  repo.AppsRepo
	conn  repo.AppsConnect
}

func expandHome(p string) (string, error) {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + rest
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("the directory must be an absolute path or start with ~")
	}
	return filepath.Clean(p), nil
}

func (s *Server) machinesOf(choices []machineChoice) (*chosen, error) {
	out := &chosen{roles: map[string]config.Role{}, networks: map[string]*config.NodeNetwork{}, live: map[string]liveNet{}}
	declared := s.declaredNodes()
	for _, ch := range choices {
		m, err := s.machineOf(ch.MAC)
		if err != nil {
			return nil, err
		}
		if d, ok := declared[netx.MACKey(m.MAC)]; ok {
			return nil, conflict(fmt.Sprintf("%s is already %s in %s's cluster.yaml.", m.IP, d.Hostname, d.Cluster))
		}
		out.machines = append(out.machines, m)
		if ch.Role != "" {
			out.roles[m.MAC] = ch.Role
		}
		var live liveNet
		if row, err := s.store.GetMachine(ch.MAC); err == nil {
			if inv, ok := inventoryOf(row); ok {
				live = liveNet{Address: inv.UplinkAddress(), Gateway: inv.Gateway, Nameservers: inv.Nameservers}
			}
		}
		out.live[m.MAC] = live
		if addr := cidrOf(ch.Address, live.Address); addr != "" {
			out.networks[m.MAC] = &config.NodeNetwork{Addresses: []string{addr}, Gateway: strings.TrimSpace(ch.Gateway), Nameservers: ch.Nameservers}
		}
	}
	return out, nil
}

const vipProbe = 1500 * time.Millisecond

func (s *Server) freeVIP(ctx context.Context, c *config.Cluster) (string, bool) {
	lan, ok := config.Slash24(c.ControlPlanes()[0].IP)
	if !ok {
		return "", false
	}
	taken := map[string]bool{}
	for _, n := range c.Spec.Nodes {
		taken[n.IP] = true
	}
	for _, m := range s.store.ListNodes("") {
		taken[m.IP] = true
	}
	var candidates []string
	for last := 250; last >= 240; last-- {
		b := lan.Addr().As4()
		b[3] = byte(last)
		if ip := netip.AddrFrom4(b).String(); !taken[ip] {
			candidates = append(candidates, ip)
		}
	}
	used := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, ip := range candidates {
		wg.Go(func() { used[i] = netx.InUse(ctx, ip, vipProbe) })
	}
	wg.Wait()
	for i, ip := range candidates {
		if !used[i] {
			return ip, true
		}
	}
	return "", false
}

func (s *Server) design(ctx context.Context, req designRequest, probe bool) (*design, error) {
	if len(req.Machines) == 0 {
		return nil, badRequest("Select at least one machine.")
	}
	ch, err := s.machinesOf(req.Machines)
	if err != nil {
		return nil, err
	}
	ms, roles := ch.machines, ch.roles
	d := &design{}
	var warnings []config.Warning
	var redesign func(vip string) *config.Cluster
	pickVIP := false
	if req.Cluster == "" {
		dir, err := expandHome(req.Dir)
		if err != nil {
			return nil, invalid(err)
		}
		if _, err := os.Stat(filepath.Join(dir, repo.ClusterFile)); err == nil {
			return nil, conflict(dir + " already has a cluster.yaml; pick another directory.")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = config.NameFrom(filepath.Base(dir))
		}
		if err := config.CheckName(name); err != nil {
			return nil, invalid(err)
		}
		vip := strings.TrimSpace(req.VIP)
		if req.Apps != nil && req.Apps.Dir != "" {
			rv, a, conn, err := reviewApps(ctx, *req.Apps, name, "", false)
			if err != nil {
				return nil, err
			}
			d.view.Apps, d.apps, d.conn = rv, a, conn
		}
		redesign = func(vip string) *config.Cluster {
			c, _ := config.Design(name, ms, config.DesignOptions{Roles: roles, VIP: vip, Networks: ch.networks})
			if rv := d.view.Apps; rv != nil {
				c.Spec.Platform.Flux = config.Flux{Enabled: true, Repository: &config.FluxRepository{URL: rv.URL, Branch: rv.Branch, Path: rv.Path, Checkout: repo.RelCheckout(dir, rv.Repo.Dir)}}
			}
			return c
		}
		c := redesign(vip)
		pickVIP = vip == ""
		warnings = config.Lint(c, ms)
		d.spec, d.added = c, c.Spec.Nodes
		d.view = designView{Cluster: name, Dir: dir, New: true, Apps: d.view.Apps}
	} else {
		dir, err := s.repoOf(req.Cluster)
		if err != nil {
			return nil, err
		}
		c, spec, err := repo.LoadSpec(dir)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			role := roles[m.MAC]
			if role == "" {
				role = config.RoleWorker
			}
			n := c.NodeFor(m, c.NextHostname(role), role)
			n.Network = ch.networks[m.MAC]
			c.Spec.Nodes = append(c.Spec.Nodes, n)
			d.added = append(d.added, n)
		}
		warnings = config.Lint(c, ms)
		d.spec = c
		d.view = designView{Cluster: c.Metadata.Name, Dir: dir, Hash: repo.Fingerprint(spec)}
	}
	if err := d.spec.Validate(); err != nil {
		return nil, invalid(err)
	}
	d.view.Warnings = []string{}
	d.view.Talos, d.view.K8s = d.spec.Spec.TalosVersion, d.spec.Spec.KubernetesVersion
	for _, w := range warnings {
		d.view.Warnings = append(d.view.Warnings, w.Message)
	}
	size := map[string]uint64{}
	for _, m := range ms {
		if len(m.Disks) > 0 {
			size[m.MAC] = m.Disks[0].SizeBytes
		}
	}
	d.view.Nodes = make([]designNode, len(d.added))
	var wg sync.WaitGroup
	for i, n := range d.added {
		d.view.Nodes[i] = designNode{Hostname: n.Hostname, IP: n.IP, MAC: n.MAC, Role: n.Role, Disk: n.InstallDisk.String(), DiskBytes: size[n.MAC], Network: n.Network, Live: ch.live[n.MAC]}
		if target := n.TargetIP(); probe && target != n.IP {
			wg.Go(func() { d.view.Nodes[i].InUse = netx.InUse(ctx, target, vipProbe) })
		}
	}
	vip := d.spec.Spec.ControlPlane.VIP
	free, found := "", false
	if probe && d.view.New && vip != "" {
		if pickVIP {
			wg.Go(func() { free, found = s.freeVIP(ctx, d.spec) })
		} else {
			wg.Go(func() { d.view.VIPInUse = netx.InUse(ctx, vip, vipProbe) })
		}
	}
	wg.Wait()
	switch {
	case found && free != vip:
		d.spec = redesign(free)
		d.added = d.spec.Spec.Nodes
	case pickVIP && probe && vip != "" && !found:
		d.view.VIPInUse = true
	}
	cp := d.spec.Spec.ControlPlane
	d.view.VIP, d.view.Endpoint = cp.VIP, cp.Endpoint
	return d, nil
}

func decode[T any](w http.ResponseWriter, r *http.Request, into *T) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeErr(w, badRequest("invalid JSON body: "+err.Error()))
		return false
	}
	return true
}

func (s *Server) handleDesign(w http.ResponseWriter, r *http.Request) {
	var req designRequest
	if !decode(w, r, &req) {
		return
	}
	d, err := s.design(r.Context(), req, strings.HasSuffix(r.URL.Path, "/checks"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d.view)
}

func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	var req designRequest
	if !decode(w, r, &req) {
		return
	}
	req.Cluster = ""
	if strings.TrimSpace(req.Dir) == "" {
		writeErr(w, badRequest("A repo directory is required."))
		return
	}
	d, err := s.design(r.Context(), req, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	recipients, _, err := repo.Recipients()
	if err != nil {
		writeErr(w, err)
		return
	}
	created, err := repo.Init(d.view.Dir, d.spec, recipients)
	if err != nil {
		writeErr(w, invalid(err))
		return
	}
	if d.view.Apps != nil {
		d.conn.FluxRecipient = created.Secrets.FluxRecipient()
		if err := s.writeApps(r.Context(), d.view.Dir, "", d.apps, d.conn); err != nil {
			writeErr(w, editErr(err))
			return
		}
	}
	if _, err := repo.GitInit(r.Context(), d.view.Dir); err != nil {
		log.Printf("repo %s: git init: %v", d.view.Dir, err)
	}
	desired, err := s.serveRepo(d.view.Dir)
	if err != nil {
		writeErr(w, err)
		return
	}
	if desired == nil {
		writeErr(w, fmt.Errorf("%s was written but cannot be read back", d.view.Dir))
		return
	}
	s.manager.Declare(desired)
	s.refreshLoaded(d.view.Cluster)
	writeJSON(w, http.StatusOK, d.view)
}

func (s *Server) handleAddNodes(w http.ResponseWriter, r *http.Request) {
	var req designRequest
	if !decode(w, r, &req) {
		return
	}
	req.Cluster = r.PathValue("name")
	d, err := s.design(r.Context(), req, false)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := repo.AddNodes(d.view.Dir, req.Hash, d.added...); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeJSON(w, http.StatusOK, d.view)
}

func (s *Server) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	dir, err := s.repoOf(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := repo.RemoveNode(dir, r.URL.Query().Get("hash"), r.PathValue("hostname")); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, dir)
}

type nodeNetworkView struct {
	Hostname        string              `json:"hostname"`
	Role            config.Role         `json:"role"`
	IP              string              `json:"ip"`
	Declared        *config.NodeNetwork `json:"declared,omitempty"`
	Live            liveNet             `json:"live"`
	Endpoint        string              `json:"endpoint"`
	EndpointFollows bool                `json:"endpointFollows"`
	ClusterDNS      []string            `json:"clusterNameservers,omitempty"`
	Prefix          int                 `json:"prefix"`
	Hash            string              `json:"hash"`
}

func prefixOf(cidrs ...string) int {
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err == nil {
			return p.Bits()
		}
	}
	return 24
}

type nodeSpec struct {
	dir  string
	c    *config.Cluster
	n    config.Node
	hash string
}

func (s *Server) nodeNetwork(r *http.Request) (nodeSpec, error) {
	name, host := r.PathValue("name"), r.PathValue("hostname")
	dir, err := s.repoOf(name)
	if err != nil {
		return nodeSpec{}, err
	}
	c, spec, err := repo.LoadSpec(dir)
	if err != nil {
		return nodeSpec{}, err
	}
	i := c.NodeIndex(host)
	if i < 0 {
		return nodeSpec{}, &statusError{Status: http.StatusNotFound, Msg: host + " is not in cluster.yaml"}
	}
	return nodeSpec{dir: dir, c: c, n: c.Spec.Nodes[i], hash: repo.Fingerprint(spec)}, nil
}

func endpointFollows(c *config.Cluster, n config.Node) bool {
	host := config.EndpointHost(c.Spec.ControlPlane.Endpoint)
	return n.Role == config.RoleControlPlane && c.Spec.ControlPlane.VIP == "" && (host == n.IP || host == n.TargetIP())
}

func (s *Server) handleNodeNetwork(w http.ResponseWriter, r *http.Request) {
	ns, err := s.nodeNetwork(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, n := ns.c, ns.n
	v := nodeNetworkView{Hostname: n.Hostname, Role: n.Role, IP: n.IP, Declared: n.Network, Endpoint: c.Spec.ControlPlane.Endpoint, EndpointFollows: endpointFollows(c, n), ClusterDNS: c.Spec.Network.Nameservers, Hash: ns.hash}
	row, err := s.store.GetMachine(n.MAC)
	if err != nil {
		row, err = s.store.GetNode(n.IP)
	}
	if err == nil {
		if inv, ok := inventoryOf(row); ok {
			v.Live = liveNet{Address: inv.UplinkAddress(), Gateway: inv.Gateway, Nameservers: inv.Nameservers}
		}
	}
	declared := ""
	if n.Network != nil && len(n.Network.Addresses) > 0 {
		declared = n.Network.Addresses[0]
	}
	v.Prefix = prefixOf(declared, v.Live.Address)
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleNodeNetworkPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Static      bool     `json:"static"`
		Address     string   `json:"address"`
		Gateway     string   `json:"gateway"`
		Nameservers []string `json:"nameservers"`
		Hash        string   `json:"hash"`
	}
	if !decode(w, r, &req) {
		return
	}
	ns, err := s.nodeNetwork(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	dir, c, n := ns.dir, ns.c, ns.n
	var nn *config.NodeNetwork
	target := n.IP
	if req.Static {
		addr := cidrOf(req.Address, "")
		pfx, err := netip.ParsePrefix(addr)
		if err != nil {
			writeErr(w, invalid(fmt.Errorf("address %q: use CIDR notation, e.g. 192.168.5.51/24", req.Address)))
			return
		}
		nn = &config.NodeNetwork{Addresses: []string{addr}, Gateway: strings.TrimSpace(req.Gateway), Nameservers: req.Nameservers}
		if n.Network != nil {
			nn.VLAN, nn.MTU = n.Network.VLAN, n.Network.MTU
		}
		target = pfx.Addr().String()
	}
	endpoint := ""
	if endpointFollows(c, n) && target != config.EndpointHost(c.Spec.ControlPlane.Endpoint) {
		u, _ := url.Parse(c.Spec.ControlPlane.Endpoint)
		port := u.Port()
		if port == "" {
			port = "6443"
		}
		endpoint = "https://" + net.JoinHostPort(target, port)
	}
	if err := repo.SetNodeNetwork(dir, req.Hash, n.Hostname, nn, endpoint); err != nil {
		writeErr(w, editErr(err))
		return
	}
	writeHash(w, dir)
}
