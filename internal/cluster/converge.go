package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/gitremote"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/netx"
	"github.com/mikaelhug/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type Desired struct {
	Dir         string
	Cluster     *config.Cluster
	Bundle      *secrets.Bundle
	Talosconfig []byte
	Kubeconfig  []byte
	FluxKey     string
	GitKey      string
	KnownHosts  string
	RepoState   string
	Passphrase  string
	BackupKey   string
	BackupSec   string
	Digest      string
}

const (
	ActCreate     = "create"
	ActAdd        = "add"
	ActConfig     = "config"
	ActTalos      = "upgrade talos"
	ActKubernetes = "upgrade kubernetes"
	ActRemove     = "remove"
	ActPlatform   = "platform"
	ActAddress    = "address"
	ActEndpoint   = "endpoint"
)

type Change struct {
	Action  string `json:"action"`
	Target  string `json:"target,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Blocked string `json:"blocked,omitempty"`
}

type Install struct {
	Hostname string      `json:"hostname"`
	Role     config.Role `json:"role"`
	IP       string      `json:"ip"`
	Disk     string      `json:"disk"`
}

type ConfigStatus struct {
	Behind []string `json:"behind"`
}

func (p *Plan) ConfigStatus() ConfigStatus {
	st := ConfigStatus{Behind: []string{}}
	for _, c := range p.Changes {
		if c.Action == ActConfig {
			st.Behind = append(st.Behind, c.Target)
		}
	}
	return st
}

func (c Change) OneTime() bool { return c.Action == ActPlatform && c.Target == "state" }

type Plan struct {
	Hash     string    `json:"hash"`
	Cluster  string    `json:"cluster"`
	Changes  []Change  `json:"changes"`
	Installs []Install `json:"installs,omitempty"`
	Problems []string  `json:"problems,omitempty"`
	applied  *config.Cluster
	target   *config.Cluster
	adds     []config.Node
	removes  []string
	moves    []addrChange
}

func (p *Plan) Empty() bool { return len(p.Changes) == 0 && len(p.Problems) == 0 }

func (p *Plan) has(action string) bool {
	return slices.ContainsFunc(p.Changes, func(c Change) bool { return c.Action == action })
}

type ConvergeOptions struct {
	AllowRemoval bool
	ReadOnly     bool
}

type liveNode struct {
	addr   string
	member bool
	maint  bool
	image  nodeImage
	live   config.LiveConfig
	err    error
}

type liveState struct {
	exists   bool
	api      bool
	etcd     bool
	endpoint string
	nodes    map[string]*liveNode
	kube     map[string]k8s.NodeStatus
}

func (m *Manager) Plan(ctx context.Context, d *Desired, opts ConvergeOptions) (*Plan, error) {
	p, err := m.plan(ctx, d, opts)
	if err == nil {
		p.seal(d.Digest)
	}
	return p, err
}

func (p *Plan) seal(digest string) {
	changes := slices.Clone(p.Changes)
	for i := range changes {
		changes[i].Blocked = ""
	}
	b, _ := json.Marshal(struct {
		Digest   string
		Changes  []Change
		Installs []Install
		Problems []string
	}{digest, changes, p.Installs, p.Problems})
	sum := sha256.Sum256(b)
	p.Hash = hex.EncodeToString(sum[:12])
}

func (m *Manager) plan(ctx context.Context, d *Desired, opts ConvergeOptions) (*Plan, error) {
	c := d.Cluster
	name := c.Metadata.Name
	p := &Plan{Cluster: name, Changes: []Change{}}
	if err := CheckDeclaration(c, d.Bundle); err != nil {
		p.Problems = append(p.Problems, "cluster.yaml: "+err.Error())
		return p, nil
	}
	if c.Spec.Backup.Enabled() && (d.BackupKey == "" || d.BackupSec == "") {
		p.Problems = append(p.Problems, "backup is declared but secrets.sops.yaml has no backup.accessKeyID and backup.secretAccessKey; add them with sops")
		return p, nil
	}
	if c.Spec.Platform.Flux.Enabled && d.FluxKey == "" {
		p.Problems = append(p.Problems, "Flux is on but secrets.sops.yaml has no flux.ageKey; add one with sops")
		return p, nil
	}
	if r := c.Spec.Platform.Flux.Repository; c.Spec.Platform.Flux.Enabled && r != nil && gitremote.IsSSH(r.URL) && d.GitKey == "" {
		p.Problems = append(p.Problems, AppsProblem+"secrets.sops.yaml has no flux.deployKey; generate one in Settings › Apps or with kubit deploy-key")
		return p, nil
	}
	p.Problems = append(p.Problems, m.fluxSourceProblems(ctx, c, d)...)
	ls := m.observe(ctx, d)
	if ls.endpoint != "" {
		m.pinEndpoint(d.Cluster.Metadata.Name, ls.endpoint)
	}
	return m.planWith(ctx, d, p, ls, opts)
}

func (m *Manager) planWith(ctx context.Context, d *Desired, p *Plan, ls *liveState, opts ConvergeOptions) (*Plan, error) {
	c := d.Cluster
	name := c.Metadata.Name
	if ls.exists && !ls.api && !ls.etcd {
		return p.resume(c, ls), nil
	}
	if !ls.exists {
		for _, n := range c.Spec.Nodes {
			if ln := ls.nodes[n.Hostname]; !ln.maint {
				p.Problems = append(p.Problems, notInMaintenance(n, n.IP, ln))
			}
		}
		if len(p.Problems) > 0 {
			return p, nil
		}
		p.Problems = append(p.Problems, takenTargets(ctx, c.Spec.Nodes, ls)...)
		if v := c.Spec.ControlPlane.VIP; v != "" && netx.InUse(ctx, v, 3*time.Second) {
			p.Problems = append(p.Problems, fmt.Sprintf("the control plane VIP %s is already in use on the network; set a free address in cluster.yaml", v))
		}
		detail := fmt.Sprintf("%d control plane(s), %d worker(s), Talos %s, Kubernetes %s", len(c.ControlPlanes()), len(c.Workers()), c.Spec.TalosVersion, c.Spec.KubernetesVersion)
		p.target = c.Clone()
		for i, n := range p.target.Spec.Nodes {
			p.target.Spec.Nodes[i].IP = p.addressOf(n, ls.nodes[n.Hostname])
			p.install(p.target.Spec.Nodes[i])
		}
		if len(p.Problems) > 0 {
			return p, nil
		}
		p.Changes = append(p.Changes, Change{Action: ActCreate, Target: name, Detail: detail})
		if c.Spec.Platform.AddOns() {
			p.Changes = append(p.Changes, Change{Action: ActPlatform, Detail: "install the add-ons"})
		}
		return p, nil
	}
	if !ls.api {
		endpoint := c.Spec.ControlPlane.Endpoint
		if ls.endpoint != "" {
			endpoint = ls.endpoint
		}
		p.Problems = append(p.Problems, "the Kubernetes API at "+endpoint+" does not answer")
		return p, nil
	}
	p.applied = appliedSpec(c, ls)
	if cps := len(c.ControlPlanes()); cps < len(p.applied.ControlPlanes()) && cps < 3 {
		p.Problems = append(p.Problems, fmt.Sprintf("cluster.yaml leaves %d control plane(s); etcd needs 3 to survive a failure", cps))
	}
	for _, n := range c.Spec.Nodes {
		ln := ls.nodes[n.Hostname]
		switch {
		case ln.member:
		case ln.maint:
			n.IP = p.addressOf(n, ln)
			p.adds = append(p.adds, n)
			p.install(n)
			p.Changes = append(p.Changes, Change{Action: ActAdd, Target: n.Hostname, Detail: fmt.Sprintf("%s at %s", n.Role, ln.addr)})
		default:
			p.Problems = append(p.Problems, notInMaintenance(n, n.TargetIP(), ln))
		}
	}
	p.Problems = append(p.Problems, takenTargets(ctx, p.adds, ls)...)
	m.planAddresses(ctx, p, d, ls)
	if len(p.applied.Spec.Nodes) > 0 {
		if err := m.EnsureSchematic(ctx, p.applied); err != nil {
			return nil, err
		}
		drift, err := m.configDrift(ctx, p.applied, d)
		if err != nil {
			return nil, err
		}
		for _, h := range sortedKeys(drift) {
			switch nd := drift[h]; {
			case nd.err != nil:
				p.Problems = append(p.Problems, fmt.Sprintf("%s: dry run failed: %v", h, nd.err))
			case nd.layout != "":
				p.Problems = append(p.Problems, fmt.Sprintf("%s: the %s volume is fixed at install; keep its storage settings or add a patch that restores them", h, nd.layout))
			case p.moving(h) && nd.reboot:
				p.Problems = append(p.Problems, fmt.Sprintf("%s: other changes in its config need a reboot; apply them before changing its address", h))
			case p.moving(h):
			default:
				p.Changes = append(p.Changes, Change{Action: ActConfig, Target: h, Detail: nd.diff})
			}
		}
	}
	if from := p.applied.Spec.TalosVersion; from != c.Spec.TalosVersion {
		p.Changes = append(p.Changes, Change{Action: ActTalos, Detail: from + " → " + c.Spec.TalosVersion})
	} else if id, err := m.Factory.CreateSchematic(ctx, c.Spec.Extensions); err == nil && id != p.applied.Spec.SchematicID {
		p.Changes = append(p.Changes, Change{Action: ActTalos, Detail: "new image for extensions " + strings.Join(c.Spec.Extensions, ", ")})
	}
	if from := p.applied.Spec.KubernetesVersion; from != c.Spec.KubernetesVersion {
		p.Changes = append(p.Changes, Change{Action: ActKubernetes, Detail: from + " → " + c.Spec.KubernetesVersion})
	}
	for _, h := range sortedKeys(ls.kube) {
		if c.NodeIndex(h) >= 0 {
			continue
		}
		ch := Change{Action: ActRemove, Target: h, Detail: "drain, delete and reset"}
		if !opts.AllowRemoval {
			ch.Blocked = "needs --allow-removal"
		}
		p.removes = append(p.removes, h)
		p.Changes = append(p.Changes, ch)
	}
	if ls.api && len(p.Problems) == 0 {
		groups, err := m.planPlatformFor(ctx, d, p.applied, opts.ReadOnly)
		if err != nil {
			p.Problems = append(p.Problems, "platform: "+err.Error())
		}
		for _, g := range groups {
			p.Changes = append(p.Changes, Change{Action: ActPlatform, Target: g.Addon, Detail: g.Actions})
		}
	}
	return p, nil
}

func (p *Plan) resume(c *config.Cluster, ls *liveState) *Plan {
	installed := 0
	for _, n := range c.Spec.Nodes {
		if ls.nodes[n.Hostname].member {
			installed++
		}
	}
	p.target = c.Clone()
	for i, n := range p.target.Spec.Nodes {
		if ln := ls.nodes[n.Hostname]; ln.maint {
			p.target.Spec.Nodes[i].IP = p.addressOf(n, ln)
			p.install(p.target.Spec.Nodes[i])
		} else if !ln.member {
			p.Problems = append(p.Problems, notInMaintenance(n, n.TargetIP(), ln))
		}
	}
	if len(p.Problems) == 0 {
		p.Changes = append(p.Changes, Change{Action: ActCreate, Target: c.Metadata.Name, Detail: fmt.Sprintf("resume: %d of %d nodes installed, etcd not running", installed, len(c.Spec.Nodes))})
	}
	return p
}

func (m *Manager) etcdRunning(ctx context.Context, d *Desired, ls *liveState) bool {
	for _, n := range d.Cluster.ControlPlanes() {
		ln := ls.nodes[n.Hostname]
		if !ln.member {
			continue
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		tc, err := talos.Dial(call, ln.addr, d.Talosconfig)
		if err == nil {
			_, err = tc.EtcdMemberCount(call)
			tc.Close()
		}
		cancel()
		if err == nil {
			return true
		}
	}
	return false
}

func (p *Plan) install(n config.Node) {
	p.Installs = append(p.Installs, Install{Hostname: n.Hostname, Role: n.Role, IP: n.IP, Disk: n.InstallDisk.String()})
}

func (p *Plan) addressOf(n config.Node, ln *liveNode) string {
	if ln.addr != n.IP && n.TargetIP() == n.IP {
		p.Problems = append(p.Problems, fmt.Sprintf("%s is declared at %s but answers at %s; update cluster.yaml or pin a static address", n.Hostname, n.IP, ln.addr))
	}
	return ln.addr
}

func notInMaintenance(n config.Node, ip string, ln *liveNode) string {
	var other otherMachine
	if errors.As(ln.err, &other) || errors.Is(ln.err, errOtherCluster) {
		return fmt.Sprintf("%s (%s) %v", n.Hostname, ip, ln.err)
	}
	return fmt.Sprintf("%s (%s) %s; boot it into Talos maintenance mode", n.Hostname, ip, talos.Describe(ln.err))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (ls *liveState) inMaintenance() bool {
	for _, ln := range ls.nodes {
		if ln.maint {
			return true
		}
	}
	return false
}

func (m *Manager) observe(ctx context.Context, d *Desired) *liveState {
	c := d.Cluster
	ls := &liveState{nodes: map[string]*liveNode{}, kube: map[string]k8s.NodeStatus{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	lans := &lanScans{byPrefix: map[netip.Prefix]*lanScan{}}
	for _, n := range c.Spec.Nodes {
		wg.Go(func() {
			ln := m.observeNode(ctx, n, d.Talosconfig, lans)
			mu.Lock()
			ls.nodes[n.Hostname] = ln
			mu.Unlock()
		})
	}
	wg.Wait()
	for _, n := range c.ControlPlanes() {
		if ln := ls.nodes[n.Hostname]; ln.member && ln.live.Endpoint != "" {
			ls.endpoint = ln.live.Endpoint
			break
		}
	}
	if kc, err := m.KubeClientFor(c.Metadata.Name, m.followEndpoint(d, ls.endpoint)); err == nil {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		nodes, err := kc.Nodes(call)
		cancel()
		if err == nil {
			ls.api = true
			for _, kn := range nodes {
				ls.kube[kn.Name] = kn
			}
		}
	}
	ls.exists = ls.api || slices.ContainsFunc(c.Spec.Nodes, func(n config.Node) bool { return ls.nodes[n.Hostname].member })
	ls.etcd = ls.api || (ls.exists && m.etcdRunning(ctx, d, ls))
	return ls
}

func (m *Manager) observeNode(ctx context.Context, n config.Node, talosconfig []byte, lans *lanScans) *liveNode {
	ln := &liveNode{}
	if m.memberAt(ctx, ln, n, n.TargetIP(), talosconfig) {
		return ln
	}
	if n.TargetIP() != n.IP && m.memberAt(ctx, ln, n, n.IP, talosconfig) {
		return ln
	}
	r := talos.Probe(ctx, n.IP, 3*time.Second)
	if r.Err == nil && r.State == talos.StateMaintenance && sameMachine(r, n) {
		ln.settle(r, n.IP)
		return ln
	}
	ln.err = r.Err
	if r.Err == nil {
		ln.err = otherMachine{ip: n.IP}
		if r.Inventory != nil {
			ln.err = otherMachine{ip: n.IP, mac: r.Inventory.PrimaryMAC()}
		}
		if r.State != talos.StateMaintenance && n.MAC == "" {
			ln.err = errOtherCluster
		}
	}
	lan, ok := config.Slash24(n.IP)
	if !ok {
		return ln
	}
	for _, s := range lans.of(ctx, lan) {
		if s.Err != nil || s.IP == n.IP || s.IP == n.TargetIP() {
			continue
		}
		if s.State == talos.StateMaintenance && n.MAC != "" && sameMachine(s, n) {
			ln.settle(s, s.IP)
			return ln
		}
		if s.State != talos.StateMaintenance && m.memberAt(ctx, ln, n, s.IP, talosconfig) {
			return ln
		}
	}
	return ln
}

func (m *Manager) memberAt(ctx context.Context, ln *liveNode, n config.Node, ip string, talosconfig []byte) bool {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tc, err := talos.Dial(call, ip, talosconfig)
	if err != nil {
		return false
	}
	defer tc.Close()
	if stage, err := tc.Stage(call); err != nil || stage == "maintenance" {
		return false
	}
	if h, err := tc.Hostname(call); err != nil || h != n.Hostname {
		return false
	}
	ln.member, ln.addr, ln.err = true, ip, nil
	if cfg, err := tc.MachineConfig(call); err == nil {
		ln.live, _ = config.ReadLive(cfg)
	}
	img, cancelImg := context.WithTimeout(ctx, 20*time.Second)
	defer cancelImg()
	ln.image, ln.err = readNodeImage(img, ip, talosconfig)
	return true
}

var errOtherCluster = errors.New("runs Talos with another cluster's configuration; reset it first")

type otherMachine struct{ ip, mac string }

func (e otherMachine) Error() string {
	if e.mac == "" {
		return fmt.Sprintf("is not on the network; another machine answers at %s", e.ip)
	}
	return fmt.Sprintf("is not on the network; %s now answers as %s", e.ip, e.mac)
}

func (ln *liveNode) settle(r talos.ScanResult, ip string) {
	ln.maint, ln.addr, ln.err = true, ip, nil
}

func sameMachine(r talos.ScanResult, n config.Node) bool {
	if n.MAC == "" {
		return true
	}
	return r.Inventory != nil && netx.MACKey(r.Inventory.PrimaryMAC()) == netx.MACKey(n.MAC)
}

type lanScan struct {
	once  sync.Once
	found []talos.ScanResult
}

type lanScans struct {
	mu       sync.Mutex
	byPrefix map[netip.Prefix]*lanScan
}

func (l *lanScans) of(ctx context.Context, lan netip.Prefix) []talos.ScanResult {
	l.mu.Lock()
	s, ok := l.byPrefix[lan]
	if !ok {
		s = &lanScan{}
		l.byPrefix[lan] = s
	}
	l.mu.Unlock()
	s.once.Do(func() {
		if addrs, err := talos.ExpandTargets([]string{lan.String()}); err == nil {
			s.found = talos.Scan(ctx, addrs, 64, 2*time.Second)
		}
	})
	return s.found
}

func appliedSpec(d *config.Cluster, ls *liveState) *config.Cluster {
	a := d.Clone()
	a.Spec.Nodes = nil
	var talosVersions, kubelets []string
	schematic := ""
	for _, n := range d.Spec.Nodes {
		ln, ok := ls.nodes[n.Hostname]
		if !ok || !ln.member {
			continue
		}
		n.IP = ln.addr
		a.Spec.Nodes = append(a.Spec.Nodes, n)
		if ln.image.version != "" {
			talosVersions = append(talosVersions, ln.image.version)
		}
		if ln.image.schematic != "" && schematic == "" {
			schematic = ln.image.schematic
		}
		if kn, ok := ls.kube[n.Hostname]; ok && kn.KubeletVersion != "" {
			kubelets = append(kubelets, kn.KubeletVersion)
		}
	}
	for _, h := range sortedKeys(ls.kube) {
		if d.NodeIndex(h) >= 0 {
			continue
		}
		a.Spec.Nodes = append(a.Spec.Nodes, undeclaredNode(h, ls.kube[h]))
	}
	if v := lowest(talosVersions); v != "" {
		a.Spec.TalosVersion = v
	}
	if v := lowest(kubelets); v != "" {
		a.Spec.KubernetesVersion = v
	}
	a.Spec.SchematicID = schematic
	return a
}

func undeclaredNode(hostname string, kn k8s.NodeStatus) config.Node {
	role := config.RoleWorker
	if _, ok := kn.Labels["node-role.kubernetes.io/control-plane"]; ok {
		role = config.RoleControlPlane
	}
	return config.Node{Hostname: hostname, IP: kn.InternalIP, Role: role, Arch: config.Arch(kn.Labels["kubernetes.io/arch"])}
}

func lowest(versions []string) string {
	var best *utilversion.Version
	var out string
	for _, v := range versions {
		pv, err := utilversion.ParseGeneric(v)
		if err != nil {
			continue
		}
		if best == nil || pv.LessThan(best) {
			best, out = pv, v
		}
	}
	if out != "" && !strings.HasPrefix(out, "v") {
		out = "v" + out
	}
	return out
}

type nodeDrift struct {
	diff, layout string
	reboot       bool
	err          error
}

func (m *Manager) configDrift(ctx context.Context, a *config.Cluster, d *Desired) (map[string]nodeDrift, error) {
	gen, err := m.machineConfigs(a, d.Bundle)
	if err != nil {
		return nil, err
	}
	out := map[string]nodeDrift{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range a.Spec.Nodes {
		cfg, ok := gen.Nodes[n.Hostname]
		if !ok {
			continue
		}
		wg.Go(func() {
			nd := driftOf(ctx, n.IP, cfg, d.Talosconfig)
			if nd.err == nil && nd.diff == "" {
				return
			}
			mu.Lock()
			out[n.Hostname] = nd
			mu.Unlock()
		})
	}
	wg.Wait()
	return out, nil
}

func driftOf(ctx context.Context, ip string, cfg, talosconfig []byte) nodeDrift {
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return nodeDrift{err: err}
	}
	defer tc.Close()
	details, err := applyDryRun(ctx, tc, cfg)
	if err != nil {
		return nodeDrift{err: err}
	}
	nd := nodeDrift{diff: configDiff(details), reboot: wantReboot(details)}
	if nd.diff == "" {
		return nd
	}
	call, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	live, err := tc.MachineConfig(call)
	if err != nil {
		return nodeDrift{err: err}
	}
	if nd.layout, err = config.LayoutChange(live, cfg); err != nil {
		return nodeDrift{err: err}
	}
	return nd
}

func configDiff(details string) string {
	_, diff, ok := strings.Cut(details, "Config diff:")
	if !ok {
		return strings.TrimSpace(details)
	}
	diff = strings.TrimSpace(diff)
	if diff == "" || strings.EqualFold(diff, "No changes.") {
		return ""
	}
	return diff
}

func (m *Manager) Converge(ctx context.Context, d *Desired, p *Plan, opts ConvergeOptions, sink Sink) error {
	if len(p.Problems) > 0 {
		return errors.New(strings.Join(p.Problems, "\n"))
	}
	for _, ch := range p.Changes {
		if ch.Action == ActRemove && !opts.AllowRemoval {
			return fmt.Errorf("plan removes %s; pass --allow-removal", ch.Target)
		}
	}
	c := d.Cluster
	name := c.Metadata.Name
	m.converging.Store(name, true)
	defer m.converging.Delete(name)
	m.use(d)
	if p.has(ActCreate) {
		if err := m.Create(ctx, p.target, d.Bundle, sink); err != nil {
			return err
		}
		return m.ApplyPlatform(ctx, name, sink)
	}
	if err := m.adopt(ctx, d, p.applied, true); err != nil {
		return err
	}
	if err := m.ApplyAddresses(ctx, d, p, sink); err != nil {
		return err
	}
	for _, n := range p.adds {
		if err := m.AddNode(ctx, name, n, sink); err != nil {
			return fmt.Errorf("add %s: %w", n.Hostname, err)
		}
	}
	if p.has(ActConfig) {
		cur, _, err := m.LoadCluster(name)
		if err != nil {
			return err
		}
		if err := m.ApplyConfigs(ctx, cur, "", sink); err != nil {
			return err
		}
	}
	if p.has(ActTalos) {
		if err := m.UpgradeTalos(ctx, name, c.Spec.TalosVersion, sink); err != nil {
			return err
		}
	}
	if p.has(ActKubernetes) {
		if err := m.UpgradeKubernetes(ctx, name, c.Spec.KubernetesVersion, sink); err != nil {
			return err
		}
	}
	for _, h := range p.removes {
		if err := m.RemoveNode(ctx, name, h, sink); err != nil {
			return fmt.Errorf("remove %s: %w", h, err)
		}
	}
	return m.ApplyPlatform(ctx, name, sink)
}

func (m *Manager) Register(d *Desired) {
	name := d.Cluster.Metadata.Name
	m.use(d)
	if _, err := m.Store.GetCluster(name); err != nil {
		m.saveCluster(d.Cluster, StateConnecting)
		return
	}
	if _, busy := m.converging.Load(name); busy {
		return
	}
	if last, ok := m.observed.Load(name); ok && last.(*liveState).exists {
		m.publish(d, appliedSpec(d.Cluster, last.(*liveState)), "")
	}
}

func (m *Manager) Declare(d *Desired) {
	m.use(d)
	if row, err := m.Store.GetCluster(d.Cluster.Metadata.Name); err != nil || row.State == StateConnecting {
		m.saveCluster(d.Cluster, StateDeclared)
	}
}

func (m *Manager) Track(ctx context.Context, d *Desired) (bool, error) {
	name := d.Cluster.Metadata.Name
	if _, busy := m.converging.Load(name); busy {
		m.use(d)
		return true, nil
	}
	m.Register(d)
	ls := m.observe(ctx, d)
	m.observed.Store(name, ls)
	if ls.endpoint != "" {
		m.pinEndpoint(name, ls.endpoint)
	}
	if !ls.exists {
		if row, err := m.Store.GetCluster(name); err == nil && Live(row.State) {
			return true, nil
		}
		state := StateConnecting
		if ls.inMaintenance() {
			state = StateDeclared
		}
		m.saveCluster(d.Cluster, state)
		return false, nil
	}
	return true, m.adopt(ctx, d, appliedSpec(d.Cluster, ls), ls.api)
}

func (m *Manager) publish(d *Desired, applied *config.Cluster, state string) {
	if prev, _, err := m.LoadCluster(d.Cluster.Metadata.Name); err == nil && prev != nil {
		for _, n := range d.Cluster.Spec.Nodes {
			if i := prev.NodeIndex(n.Hostname); applied.NodeIndex(n.Hostname) < 0 && i >= 0 {
				applied.Spec.Nodes = append(applied.Spec.Nodes, prev.Spec.Nodes[i])
			}
		}
	}
	m.saveCluster(applied, state)
}

func (m *Manager) adopt(ctx context.Context, d *Desired, applied *config.Cluster, ready bool) error {
	if err := m.EnsureSchematic(ctx, applied); err != nil {
		return err
	}
	state := StateBootstrapped
	if ready {
		state = StateReady
	}
	m.publish(d, applied, state)
	for _, n := range applied.Spec.Nodes {
		if d.Cluster.NodeIndex(n.Hostname) >= 0 {
			m.recordNode(applied, n)
		}
	}
	return nil
}
