package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/netx"
	"github.com/mikaelhug/kubit/internal/store"
)

const swapSpec = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: lab }
spec:
  controlPlane: { endpoint: "https://127.0.0.10:6443", allowScheduling: true }
  network: { nameservers: [127.0.0.53] }
  nodes:
    - { hostname: cp-02, ip: 127.0.0.1, mac: "10:e7:c6:0b:7f:c6", role: controlplane, installDisk: { path: /dev/sda }, network: { addresses: [127.0.0.10/8], gateway: 127.0.0.2 } }
`

var cpLabel = map[string]string{"node-role.kubernetes.io/control-plane": ""}

func TestAnInterruptedSwapAlwaysPlansTheRemainingSteps(t *testing.T) {
	defer func() { addressInUse = netx.InUse }()
	atOne := config.Addressing{Addresses: []string{"127.0.0.1/8"}, Gateway: "127.0.0.2"}
	old := k8s.NodeStatus{Name: "cp-01", InternalIP: "127.0.0.10", Labels: cpLabel}
	cp02 := k8s.NodeStatus{Name: "cp-02", InternalIP: "127.0.0.1", Labels: cpLabel}
	for _, tc := range []struct {
		name    string
		ls      *liveState
		want    []string
		handsOn bool
	}{
		{"worker still running", &liveState{exists: true, api: true, etcd: true,
			nodes: map[string]*liveNode{"cp-02": {rejoin: &liveMachine{hostname: "w-04", role: config.RoleWorker, addr: "127.0.0.1"}}},
			kube:  map[string]k8s.NodeStatus{"cp-01": old, "w-04": {Name: "w-04", InternalIP: "127.0.0.1"}},
		}, []string{"rejoin cp-02", "address cp-02", "remove cp-01"}, true},
		{"worker reset to maintenance", &liveState{exists: true, api: true, etcd: true,
			nodes: map[string]*liveNode{"cp-02": {maint: true, addr: "127.0.0.50"}},
			kube:  map[string]k8s.NodeStatus{"cp-01": old},
		}, []string{"add cp-02", "address cp-02", "remove cp-01"}, true},
		{"new control plane joined", &liveState{exists: true, api: true, etcd: true,
			nodes: map[string]*liveNode{"cp-02": {member: true, addr: "127.0.0.1", live: config.LiveConfig{Addressing: atOne, Found: true}}},
			kube:  map[string]k8s.NodeStatus{"cp-01": old, "cp-02": cp02},
		}, []string{"address cp-02", "remove cp-01"}, true},
		{"old control plane gone", &liveState{exists: true, api: true, etcd: true, apiAt: "https://127.0.0.1:6443",
			nodes: map[string]*liveNode{"cp-02": {member: true, addr: "127.0.0.1", live: config.LiveConfig{Addressing: atOne, Found: true}}},
			kube:  map[string]k8s.NodeStatus{"cp-02": cp02},
		}, []string{"address cp-02"}, false},
	} {
		cp01Up := tc.ls.kube["cp-01"].Name != ""
		addressInUse = func(_ context.Context, ip string, _ time.Duration) bool { return cp01Up && ip == "127.0.0.10" }
		p := planFor(t, swapSpec, tc.ls, ConvergeOptions{AllowRemoval: true})
		got := slices.DeleteFunc(actions(p), func(a string) bool { return strings.HasPrefix(a, "config ") })
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: plans %v, want %v", tc.name, got, tc.want)
		}
		for _, bad := range []string{"leaves 1 control plane", "already answers", "two control planes", "does not answer", "holds the API endpoint"} {
			if hasProblem(p, bad) {
				t.Errorf("%s: %v", tc.name, p.Problems)
			}
		}
		if (p.handover != nil) != tc.handsOn {
			t.Errorf("%s: handover %+v", tc.name, p.handover)
		}
		for _, ch := range p.moves {
			if ch.to == "127.0.0.10" && ch.after != tc.handsOn {
				t.Errorf("%s: the move onto the endpoint must wait for cp-01's removal: %+v", tc.name, ch)
			}
		}
		for _, n := range append(slices.Clone(p.adds), func() (out []config.Node) {
			for _, r := range p.rejoins {
				out = append(out, r.node)
			}
			return
		}()...) {
			if n.TargetIP() == "127.0.0.10" {
				t.Errorf("%s: cp-02 must install beside cp-01, not on its address: %+v", tc.name, n.Network)
			}
		}
	}
}

func TestRemoveRefusesTheEndpointHostWithoutAVotingSuccessor(t *testing.T) {
	st := testStore(t)
	spec := strings.Replace(threeControlPlanes, "cp-03, ip: 10.0.0.3", "cp-03, ip: 127.0.0.1", 1)
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: parsedSpec(t, spec), State: StateReady})
	st.PutClusterSecrets("lab", store.ClusterSecrets{Talosconfig: talosconfigFor(t), Kubeconfig: kubeconfigFor("https://10.0.0.1:6443")})
	m := NewManager(st, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var steps []string
	sink := func(e Event) { steps = append(steps, e.Step) }
	if err := m.removeNode(ctx, "lab", "cp-01", "", sink); err == nil || !strings.Contains(err.Error(), "API endpoint") {
		t.Errorf("without a successor: %v", err)
	}
	if err := m.removeNode(ctx, "lab", "cp-01", "cp-03", sink); err == nil || !strings.Contains(err.Error(), "etcd") {
		t.Errorf("a successor whose etcd membership is unknown: %v", err)
	}
	if slices.Contains(steps, "drain") || slices.Contains(steps, "reset") {
		t.Errorf("the endpoint host was touched: %v", steps)
	}
}

func TestRemoveRefusesANodeThatStillHoldsLonghornReplicas(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/longhorn.io/v1beta2/namespaces/longhorn-system/nodes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"longhorn.io/v1beta2","kind":"NodeList","metadata":{},"items":[{"apiVersion":"longhorn.io/v1beta2","kind":"Node","metadata":{"name":"w-01","namespace":"longhorn-system"},"spec":{"disks":{"d":{"path":"/var/lib/longhorn"}}},"status":{"diskStatus":{"d":{"scheduledReplica":{"pvc-1-r":1,"pvc-2-r":1}}}}}]}`))
	}))
	defer api.Close()
	st := testStore(t)
	st.PutCluster(store.ClusterRow{Name: "lab", Spec: parsedSpec(t, convergeSpec), State: StateReady})
	st.PutClusterSecrets("lab", store.ClusterSecrets{Talosconfig: talosconfigFor(t), Kubeconfig: kubeconfigFor(api.URL)})
	m := NewManager(st, t.TempDir())
	var steps []string
	err := m.RemoveNode(context.Background(), "lab", "w-01", func(e Event) { steps = append(steps, e.Step) })
	if err == nil || !strings.Contains(err.Error(), "still holds 2 Longhorn replicas") {
		t.Errorf("a node with the only copy of two volumes: %v", err)
	}
	if slices.Contains(steps, "drain") || slices.Contains(steps, "reset") {
		t.Errorf("the node was touched: %v", steps)
	}
}
