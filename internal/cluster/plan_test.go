package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/k8s"
	"github.com/mikael/kubit/internal/talos"
)

func planManager(t *testing.T) *Manager {
	t.Helper()
	factory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"new"}`)
	}))
	t.Cleanup(factory.Close)
	m := NewManager(testStore(t), t.TempDir())
	m.Factory.SetBaseURL(factory.URL)
	return m
}

func planFor(t *testing.T, spec string, ls *liveState, opts ConvergeOptions) *Plan {
	t.Helper()
	c, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := planManager(t).planWith(ctx, &Desired{Cluster: c, Bundle: bundle(t), Talosconfig: talosconfigFor(t)}, &Plan{Cluster: c.Metadata.Name, Changes: []Change{}}, ls, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var generatedTalosconfig = sync.OnceValues(func() ([]byte, error) {
	c, err := config.Parse([]byte(threeControlPlanes))
	if err != nil {
		return nil, err
	}
	gen, err := config.Generate(c, nil, "installer")
	if err != nil {
		return nil, err
	}
	return gen.Talosconfig.Bytes()
})

func talosconfigFor(t *testing.T) []byte {
	t.Helper()
	b, err := generatedTalosconfig()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func actions(p *Plan) []string {
	var out []string
	for _, c := range p.Changes {
		out = append(out, strings.TrimSpace(c.Action+" "+c.Target))
	}
	return out
}

func hasProblem(p *Plan, text string) bool {
	return slices.ContainsFunc(p.Problems, func(s string) bool { return strings.Contains(s, text) })
}

func TestPlanRefusesACreateOnATakenVIP(t *testing.T) {
	spec := strings.Replace(convergeSpec, `controlPlane: {endpoint: "https://10.0.0.10:6443"}`, `controlPlane: {endpoint: "https://10.0.0.10:6443", vip: 127.0.0.1}`, 1)
	p := planFor(t, spec, &liveState{nodes: map[string]*liveNode{
		"cp-01": {maint: true, addr: "10.0.0.10"}, "w-01": {maint: true, addr: "10.0.0.11"}, "w-02": {maint: true, addr: "10.0.0.12"},
	}}, ConvergeOptions{})
	if !hasProblem(p, "VIP 127.0.0.1 is already in use") || len(p.Changes) != 0 {
		t.Errorf("a taken VIP blocks the create: %v %v", actions(p), p.Problems)
	}
}

func TestPlanNeverResumesACreateWhileEtcdRuns(t *testing.T) {
	p := planFor(t, convergeSpec, &liveState{exists: true, etcd: true, nodes: map[string]*liveNode{
		"cp-01": {member: true, addr: "10.0.0.10"}, "w-01": {member: true, addr: "10.0.0.11"}, "w-02": {member: true, addr: "10.0.0.12"},
	}}, ConvergeOptions{})
	if len(p.Changes) != 0 || !hasProblem(p, "does not answer") {
		t.Errorf("API down: %+v", p)
	}
}

func TestPlanRefusesAPartialMaintenanceCreate(t *testing.T) {
	p := planFor(t, convergeSpec, &liveState{nodes: map[string]*liveNode{
		"cp-01": {maint: true, addr: "10.0.0.10"}, "w-01": {}, "w-02": {maint: true, addr: "10.0.0.12"},
	}}, ConvergeOptions{})
	if len(p.Changes) != 0 || len(p.Problems) != 1 || !hasProblem(p, "w-01 (10.0.0.11) does not answer; boot it into Talos maintenance mode") {
		t.Errorf("a node that does not answer: %+v", p)
	}
}

func TestPlanBlocksNodeRemovalWithoutAllowRemoval(t *testing.T) {
	ls := &liveState{exists: true, api: true, etcd: true,
		nodes: map[string]*liveNode{
			"cp-01": {member: true, addr: "127.0.0.1", image: nodeImage{version: "v1.14.0", schematic: "old"}},
			"w-01":  {member: true, addr: "127.0.0.1", image: nodeImage{version: "v1.14.2", schematic: "old"}},
			"w-02":  {maint: true, addr: "10.0.0.12"},
		},
		kube: map[string]k8s.NodeStatus{
			"cp-01": {Name: "cp-01", KubeletVersion: "v1.35.1"},
			"w-01":  {Name: "w-01", KubeletVersion: "v1.34.2"},
			"w-09":  {Name: "w-09", InternalIP: "127.0.0.9"},
		},
	}
	p := planFor(t, convergeSpec, ls, ConvergeOptions{})
	if i := slices.IndexFunc(p.Changes, func(c Change) bool { return c.Action == ActRemove }); i < 0 || p.Changes[i].Blocked == "" {
		t.Errorf("removal needs --allow-removal: %+v", p.Changes)
	}
	allowed := planFor(t, convergeSpec, ls, ConvergeOptions{AllowRemoval: true})
	if i := slices.IndexFunc(allowed.Changes, func(c Change) bool { return c.Action == ActRemove }); i < 0 || allowed.Changes[i].Blocked != "" {
		t.Errorf("--allow-removal unblocks it: %+v", allowed.Changes)
	}
}

func TestPlanRefusesShrinkingBelowThreeControlPlanes(t *testing.T) {
	ls := &liveState{exists: true, api: true, etcd: true,
		nodes: map[string]*liveNode{
			"cp-01": {member: true, addr: "127.0.0.1"}, "w-01": {member: true, addr: "127.0.0.1"}, "w-02": {member: true, addr: "127.0.0.1"},
		},
		kube: map[string]k8s.NodeStatus{
			"cp-01": {Name: "cp-01"}, "w-01": {Name: "w-01"}, "w-02": {Name: "w-02"},
			"cp-02": {Name: "cp-02", InternalIP: "127.0.0.4", Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""}},
		},
	}
	if p := planFor(t, convergeSpec, ls, ConvergeOptions{AllowRemoval: true}); !hasProblem(p, "leaves 1 control plane") {
		t.Errorf("dropping to one control plane: %v", p.Problems)
	}
}

func parsedSpec(t *testing.T, spec string) *config.Cluster {
	t.Helper()
	c, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnotherMachineAtTheDeclaredAddressIsNotTheNode(t *testing.T) {
	n := config.Node{Hostname: "w-01", IP: "10.0.0.11", MAC: "04:0E:3C:C5:4B:D1"}
	inv := func(mac string) talos.ScanResult {
		return talos.ScanResult{IP: "10.0.0.11", State: talos.StateMaintenance, Inventory: &talos.Inventory{Links: []talos.Link{{Name: "eth0", MAC: mac, Up: true}}}}
	}
	if !sameMachine(inv("04:0e:3c:c5:4b:d1"), n) {
		t.Error("the declared MAC matches regardless of case")
	}
	if sameMachine(inv("10:e7:c6:00:00:04"), n) || sameMachine(talos.ScanResult{State: talos.StateMaintenance}, n) {
		t.Error("another or an unknown machine at the declared address is not the node")
	}
}

func TestPlanHashCoversTheRepoAndTheChanges(t *testing.T) {
	plan := func(blocked string) *Plan {
		return &Plan{Cluster: "lab", Changes: []Change{{Action: ActAdd, Target: "w-01", Detail: "worker at 10.0.0.11"}, {Action: ActRemove, Target: "w-09", Blocked: blocked}}}
	}
	a, b := plan(""), plan("needs --allow-removal")
	a.seal("d1")
	b.seal("d1")
	if a.Hash == "" || a.Hash != b.Hash {
		t.Errorf("allowing removal does not change what was reviewed: %s %s", a.Hash, b.Hash)
	}
	c := plan("")
	c.seal("d2")
	if c.Hash == a.Hash {
		t.Error("an edited cluster.yaml makes a new plan")
	}
	d := plan("")
	d.Changes[0].Detail = "worker at 10.0.0.12"
	d.seal("d1")
	if d.Hash == a.Hash {
		t.Error("a different change makes a new plan")
	}
}
