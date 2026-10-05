package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/mikael/kubit/internal/tofu"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

type Desired struct {
	Cluster     *config.Cluster
	Bundle      *secrets.Bundle
	BundleYAML  []byte
	Talosconfig []byte
	Kubeconfig  []byte
	FluxKey     string
	StatePath   string
	Passphrase  string
	BackupKey   string
	BackupSec   string
}

func (d *Desired) platform() PlatformState {
	st := PlatformState{Path: d.StatePath, Passphrase: d.Passphrase}
	if d.BackupKey != "" || d.BackupSec != "" {
		st.Env = []string{"TF_VAR_backup_access_key_id=" + d.BackupKey, "TF_VAR_backup_secret_access_key=" + d.BackupSec}
	}
	return st
}

const (
	ActCreate     = "create"
	ActAdd        = "add"
	ActConfig     = "config"
	ActTalos      = "upgrade talos"
	ActKubernetes = "upgrade kubernetes"
	ActRemove     = "remove"
	ActPlatform   = "platform"
)

type Change struct {
	Action  string `json:"action"`
	Target  string `json:"target,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Blocked string `json:"blocked,omitempty"`
}

type Plan struct {
	Cluster  string   `json:"cluster"`
	Changes  []Change `json:"changes"`
	Problems []string `json:"problems,omitempty"`
	applied  *config.Cluster
	target   *config.Cluster
	recreate bool
	adds     []config.Node
	removes  []string
}

func (p *Plan) Empty() bool { return len(p.Changes) == 0 && len(p.Problems) == 0 }

func (p *Plan) has(action string) bool {
	return slices.ContainsFunc(p.Changes, func(c Change) bool { return c.Action == action })
}

type ConvergeOptions struct {
	AllowRemoval bool
}

type liveNode struct {
	addr   string
	member bool
	maint  bool
	image  nodeImage
	err    error
}

type liveState struct {
	exists bool
	api    bool
	nodes  map[string]*liveNode
	kube   map[string]k8s.NodeStatus
}

func (m *Manager) Plan(ctx context.Context, d *Desired, opts ConvergeOptions) (*Plan, error) {
	c := d.Cluster
	name := c.Metadata.Name
	if err := m.checkCachedSecrets(ctx, d); err != nil {
		return nil, err
	}
	p := &Plan{Cluster: name, Changes: []Change{}}
	if err := CheckDeclaration(c, d.Bundle); err != nil {
		p.Problems = append(p.Problems, "cluster.yaml: "+err.Error())
		return p, nil
	}
	if c.Spec.Backup.Enabled() && (d.BackupKey == "" || d.BackupSec == "") {
		p.Problems = append(p.Problems, "backup is declared but secrets.sops.yaml has no backup.accessKeyID and backup.secretAccessKey; add them with sops")
		return p, nil
	}
	ls := m.observe(ctx, d)
	if row, err := m.Store.GetCluster(ctx, name); err == nil && !Observable(row.State) {
		p.Changes = append(p.Changes, Change{Action: ActCreate, Target: name, Detail: "resume the " + row.State + " create"})
		p.target = c
		return p, nil
	}
	if !ls.exists {
		var missing []string
		for _, n := range c.Spec.Nodes {
			if ln := ls.nodes[n.Hostname]; !ln.maint {
				missing = append(missing, fmt.Sprintf("%s (%s): %s", n.Hostname, n.IP, reason(ln)))
			}
		}
		if len(missing) > 0 {
			p.Problems = append(p.Problems, "not in maintenance mode: "+strings.Join(missing, "; "))
			return p, nil
		}
		detail := fmt.Sprintf("%d control plane(s), %d worker(s), Talos %s, Kubernetes %s", len(c.ControlPlanes()), len(c.Workers()), c.Spec.TalosVersion, c.Spec.KubernetesVersion)
		if _, err := m.Store.GetCluster(ctx, name); err == nil {
			detail = "again: every node is back in maintenance mode"
			p.recreate = true
		}
		p.target = c.Clone()
		for i, n := range p.target.Spec.Nodes {
			p.target.Spec.Nodes[i].IP = p.addressOf(n, ls.nodes[n.Hostname])
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
		p.Problems = append(p.Problems, "the Kubernetes API at "+c.Spec.ControlPlane.Endpoint+" does not answer")
		return p, nil
	}
	cached, _, _ := m.LoadCluster(ctx, name)
	p.applied = appliedSpec(c, ls, cached)
	for _, n := range c.Spec.Nodes {
		ln := ls.nodes[n.Hostname]
		switch {
		case ln.member:
		case ln.maint:
			n.IP = p.addressOf(n, ln)
			p.adds = append(p.adds, n)
			p.Changes = append(p.Changes, Change{Action: ActAdd, Target: n.Hostname, Detail: fmt.Sprintf("%s at %s", n.Role, ln.addr)})
		default:
			p.Problems = append(p.Problems, fmt.Sprintf("%s (%s): %s", n.Hostname, n.TargetIP(), reason(ln)))
		}
	}
	if len(p.applied.Spec.Nodes) > 0 {
		if err := m.EnsureSchematic(ctx, p.applied); err != nil {
			return nil, err
		}
		drift, err := m.configDrift(ctx, p.applied, d)
		if err != nil {
			return nil, err
		}
		for _, h := range sortedKeys(drift) {
			p.Changes = append(p.Changes, Change{Action: ActConfig, Target: h, Detail: drift[h]})
		}
	}
	if from := p.applied.Spec.TalosVersion; from != c.Spec.TalosVersion {
		p.Changes = append(p.Changes, Change{Action: ActTalos, Detail: from + " → " + c.Spec.TalosVersion})
	} else if id, pools, err := m.desiredSchematics(ctx, c); err == nil && imageOutdated(p.applied, id, pools) {
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
		diff, err := m.planPlatformFor(ctx, d, p.applied)
		if err != nil {
			p.Problems = append(p.Problems, "platform: "+err.Error())
		} else {
			for _, g := range diff.Groups {
				p.Changes = append(p.Changes, Change{Action: ActPlatform, Target: g.Addon, Detail: countActions(g.Changes)})
			}
		}
	}
	return p, nil
}

func (p *Plan) addressOf(n config.Node, ln *liveNode) string {
	if ln.addr != n.IP && n.TargetIP() == n.IP {
		p.Problems = append(p.Problems, fmt.Sprintf("%s is declared at %s but answers at %s; update cluster.yaml or pin a static address", n.Hostname, n.IP, ln.addr))
	}
	return ln.addr
}

func countActions(changes []tofu.Change) string {
	n := map[string]int{}
	var order []string
	for _, c := range changes {
		if n[c.Action] == 0 {
			order = append(order, c.Action)
		}
		n[c.Action]++
	}
	parts := make([]string, 0, len(order))
	for _, a := range order {
		parts = append(parts, fmt.Sprintf("%d to %s", n[a], a))
	}
	return strings.Join(parts, ", ")
}

func reason(ln *liveNode) string {
	if ln.err != nil {
		return ln.err.Error()
	}
	return "does not answer"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) observe(ctx context.Context, d *Desired) *liveState {
	c := d.Cluster
	ls := &liveState{nodes: map[string]*liveNode{}, kube: map[string]k8s.NodeStatus{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range c.Spec.Nodes {
		wg.Go(func() {
			ln := m.observeNode(ctx, n, d.Talosconfig)
			mu.Lock()
			ls.nodes[n.Hostname] = ln
			mu.Unlock()
		})
	}
	wg.Wait()
	if kc, err := m.KubeClientFor(c.Metadata.Name, &store.ClusterSecrets{Kubeconfig: d.Kubeconfig}); err == nil {
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
	return ls
}

func (m *Manager) observeNode(ctx context.Context, n config.Node, talosconfig []byte) *liveNode {
	ln := &liveNode{}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	stage, err := talos.Stage(probe, n.TargetIP(), talosconfig)
	cancel()
	if err == nil && stage != "maintenance" {
		ln.member, ln.addr = true, n.TargetIP()
		call, cancel := context.WithTimeout(ctx, 20*time.Second)
		ln.image, ln.err = readNodeImage(call, ln.addr, talosconfig)
		cancel()
		return ln
	}
	for _, ip := range m.candidateIPs(ctx, n) {
		r := talos.Probe(ctx, ip, 3*time.Second)
		switch {
		case r.Err == nil && r.State == talos.StateMaintenance:
			ln.maint, ln.addr, ln.err = true, ip, nil
			return ln
		case r.Err == nil:
			ln.err = fmt.Errorf("Talos at %s has another cluster's configuration", ip)
		case ln.err == nil:
			ln.err = r.Err
		}
	}
	return ln
}

func (m *Manager) candidateIPs(ctx context.Context, n config.Node) []string {
	ips := []string{n.IP}
	if n.MAC != "" {
		if mc, err := m.Store.GetMachine(ctx, n.MAC); err == nil && mc.IP != "" && !slices.Contains(ips, mc.IP) {
			ips = append(ips, mc.IP)
		}
	}
	return ips
}

func appliedSpec(d *config.Cluster, ls *liveState, cached *config.Cluster) *config.Cluster {
	a := d.Clone()
	a.Spec.Nodes = nil
	var talosVersions, kubelets []string
	schematics := map[string]string{}
	for _, n := range d.Spec.Nodes {
		ln := ls.nodes[n.Hostname]
		if !ln.member {
			continue
		}
		n.IP = ln.addr
		a.Spec.Nodes = append(a.Spec.Nodes, n)
		if ln.image.version != "" {
			talosVersions = append(talosVersions, ln.image.version)
		}
		if ln.image.schematic != "" {
			schematics[d.PoolOf(n).Name] = ln.image.schematic
		}
		if kn, ok := ls.kube[n.Hostname]; ok && kn.KubeletVersion != "" {
			kubelets = append(kubelets, kn.KubeletVersion)
		}
	}
	for _, h := range sortedKeys(ls.kube) {
		if d.NodeIndex(h) >= 0 {
			continue
		}
		a.Spec.Nodes = append(a.Spec.Nodes, undeclaredNode(h, ls.kube[h], cached))
	}
	if v := lowest(talosVersions); v != "" {
		a.Spec.TalosVersion = v
	}
	if v := lowest(kubelets); v != "" {
		a.Spec.KubernetesVersion = v
	}
	a.Spec.SchematicID = ""
	for i := range a.Spec.Pools {
		p := &a.Spec.Pools[i]
		p.SchematicID = ""
		if len(p.Extensions) > 0 {
			p.SchematicID = schematics[p.Name]
		} else if id := schematics[p.Name]; id != "" {
			a.Spec.SchematicID = id
		}
	}
	if a.Spec.SchematicID == "" {
		for _, n := range a.Spec.Nodes {
			if id := schematics[a.PoolOf(n).Name]; id != "" && len(a.PoolOf(n).Extensions) == 0 {
				a.Spec.SchematicID = id
				break
			}
		}
	}
	return a
}

func undeclaredNode(hostname string, kn k8s.NodeStatus, cached *config.Cluster) config.Node {
	if cached != nil {
		if i := cached.NodeIndex(hostname); i >= 0 {
			n := cached.Spec.Nodes[i]
			if kn.InternalIP != "" {
				n.IP = kn.InternalIP
			}
			return n
		}
	}
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

func (m *Manager) configDrift(ctx context.Context, a *config.Cluster, d *Desired) (map[string]string, error) {
	gen, err := config.Generate(a, d.Bundle, m.installer(a))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range a.Spec.Nodes {
		cfg, ok := gen.Nodes[n.Hostname]
		if !ok {
			continue
		}
		wg.Go(func() {
			diff, err := dryRunDiff(ctx, n.IP, cfg, d.Talosconfig)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				out[n.Hostname] = "dry run failed: " + err.Error()
			case diff != "":
				out[n.Hostname] = diff
			}
		})
	}
	wg.Wait()
	return out, nil
}

func dryRunDiff(ctx context.Context, ip string, cfg, talosconfig []byte) (string, error) {
	tc, err := talos.Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", err
	}
	defer tc.Close()
	details, err := applyDryRun(ctx, tc, cfg)
	if err != nil {
		return "", err
	}
	return configDiff(details), nil
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

func (m *Manager) checkCachedSecrets(ctx context.Context, d *Desired) error {
	name := d.Cluster.Metadata.Name
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if stored, err := config.ParseSecrets(sec.SecretsBundle); err != nil || stored.Cluster == nil {
		return nil
	}
	if !sameBundle(sec.SecretsBundle, d.Bundle) {
		return fmt.Errorf("~/.kubit holds another cluster named %s with other secrets; move it with kubit export %s --repo <dir>, or rename this one", name, name)
	}
	return nil
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
	m.UsePlatformState(name, d.platform())
	if p.has(ActCreate) {
		if p.recreate {
			if err := m.Store.DeleteCluster(ctx, name); err != nil {
				return err
			}
		}
		if err := m.cacheSecrets(ctx, d); err != nil {
			return err
		}
		if err := m.Create(ctx, p.target, d.Bundle, sink); err != nil {
			return err
		}
		return m.ApplyPlatform(ctx, name, sink)
	}
	if err := m.adopt(ctx, d, p.applied, true); err != nil {
		return err
	}
	for _, n := range p.adds {
		if err := m.AddNode(ctx, name, n, sink); err != nil {
			return fmt.Errorf("add %s: %w", n.Hostname, err)
		}
	}
	if p.has(ActConfig) {
		cur, _, err := m.LoadCluster(ctx, name)
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
		if err := m.RemoveNode(ctx, name, h, RemoveOptions{}, sink); err != nil {
			return fmt.Errorf("remove %s: %w", h, err)
		}
	}
	if err := m.ApplyPlatform(ctx, name, sink); err != nil {
		return err
	}
	return m.settle(ctx, d)
}

func (m *Manager) Track(ctx context.Context, d *Desired) (bool, error) {
	if err := m.checkCachedSecrets(ctx, d); err != nil {
		return false, err
	}
	name := d.Cluster.Metadata.Name
	m.UsePlatformState(name, d.platform())
	if row, err := m.Store.GetCluster(ctx, name); err == nil && Observable(row.State) {
		return true, m.cacheSecrets(ctx, d)
	}
	ls := m.observe(ctx, d)
	if !ls.exists {
		return false, nil
	}
	return true, m.adopt(ctx, d, appliedSpec(d.Cluster, ls, nil), ls.api)
}

func (m *Manager) adopt(ctx context.Context, d *Desired, applied *config.Cluster, ready bool) error {
	name := applied.Metadata.Name
	if err := m.EnsureSchematic(ctx, applied); err != nil {
		return err
	}
	spec, err := applied.Marshal()
	if err != nil {
		return err
	}
	state := StateBootstrapped
	if ready {
		state = StateReady
	}
	if err := m.Store.PutCluster(ctx, store.ClusterRow{Name: name, Spec: spec, SchematicID: applied.Spec.SchematicID, State: state}); err != nil {
		return err
	}
	if err := m.cacheSecrets(ctx, d); err != nil {
		return err
	}
	for _, n := range applied.Spec.Nodes {
		if d.Cluster.NodeIndex(n.Hostname) < 0 {
			continue
		}
		row := storeRow(applied, n)
		row.State = NodeReady
		if err := m.Store.UpsertNode(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) cacheSecrets(ctx context.Context, d *Desired) error {
	name := d.Cluster.Metadata.Name
	sec := store.ClusterSecrets{SecretsBundle: d.BundleYAML, Talosconfig: d.Talosconfig, Kubeconfig: d.Kubeconfig}
	if cur, err := m.Store.GetClusterSecrets(ctx, name); err == nil && cur.Talosconfig != nil && cur.Kubeconfig != nil && sameBundle(cur.SecretsBundle, d.Bundle) {
		sec.Talosconfig, sec.Kubeconfig = cur.Talosconfig, cur.Kubeconfig
	}
	m.Store.HoldClusterSecrets(name, sec)
	recipient := ""
	if d.FluxKey != "" {
		id, err := age.ParseX25519Identity(d.FluxKey)
		if err != nil {
			return fmt.Errorf("flux age key: %w", err)
		}
		recipient = id.Recipient().String()
		m.Store.HoldSOPSKey(name, keysFile(id), recipient)
	}
	return m.Store.DropStoredSecrets(ctx, name, recipient)
}

func sameBundle(raw []byte, b *secrets.Bundle) bool {
	stored, err := config.ParseSecrets(raw)
	return err == nil && stored.Cluster != nil && b != nil && b.Cluster != nil && stored.Cluster.ID == b.Cluster.ID && stored.Cluster.Secret == b.Cluster.Secret
}

func (m *Manager) settle(ctx context.Context, d *Desired) error {
	cur, row, err := m.LoadCluster(ctx, d.Cluster.Metadata.Name)
	if err != nil {
		return err
	}
	final := d.Cluster.Clone()
	final.Spec.SchematicID = cur.Spec.SchematicID
	for i := range final.Spec.Pools {
		if j := slices.IndexFunc(cur.Spec.Pools, func(p config.Pool) bool { return p.Name == final.Spec.Pools[i].Name }); j >= 0 {
			final.Spec.Pools[i].SchematicID = cur.Spec.Pools[j].SchematicID
		}
	}
	for i, n := range final.Spec.Nodes {
		if j := cur.NodeIndex(n.Hostname); j >= 0 {
			final.Spec.Nodes[i].IP = cur.Spec.Nodes[j].IP
		}
	}
	spec, err := final.Marshal()
	if err != nil {
		return err
	}
	return m.Store.PutCluster(ctx, store.ClusterRow{Name: final.Metadata.Name, Spec: spec, SchematicID: final.Spec.SchematicID, State: row.State})
}
