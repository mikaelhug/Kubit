package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
)

func editServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, st, _ := localServer(t)
	c, err := config.Parse([]byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := c.Marshal()
	if err := st.PutCluster(t.Context(), store.ClusterRow{Name: "c", Spec: spec, State: cluster.StateReady}); err != nil {
		t.Fatal(err)
	}
	return s, st
}

func formOf(t *testing.T, s *Server) clusterForm {
	t.Helper()
	c, _, err := s.manager.LoadCluster(t.Context(), "c")
	if err != nil {
		t.Fatal(err)
	}
	return clusterForm{TalosVersion: c.Spec.TalosVersion, KubernetesVersion: c.Spec.KubernetesVersion, Endpoint: c.Spec.ControlPlane.Endpoint, VIP: c.Spec.ControlPlane.VIP, AllowScheduling: c.Spec.ControlPlane.AllowScheduling, PodCIDR: c.Spec.Network.PodCIDR, ServiceCIDR: c.Spec.Network.ServiceCIDR, Extensions: c.Spec.Extensions, Nameservers: c.Spec.Network.Nameservers}
}

func TestEditWaitsForTheOperationAndKeepsItsWork(t *testing.T) {
	s, st := editServer(t)
	ctx := t.Context()
	f := formOf(t, s)
	f.NTP = []string{"time.example"}
	body, _ := json.Marshal(f)

	s.locks.lockContext(t.Context(), specLock("c"))
	result := make(chan int)
	go func() { result <- call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)).Code }()
	time.Sleep(100 * time.Millisecond)
	c, _, err := s.manager.LoadCluster(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Nodes = append(c.Spec.Nodes, config.Node{Hostname: "b", IP: "10.0.0.2", Role: config.RoleWorker, InstallDisk: config.InstallDisk{Path: "/dev/sda"}})
	if err := s.manager.SaveCluster(ctx, c, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClusterState(ctx, "c", cluster.StateBootstrapped); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-result:
		t.Fatalf("the edit ran while the operation held the cluster: %d", code)
	default:
	}
	s.locks.unlock(specLock("c"))
	if code := <-result; code != http.StatusOK {
		t.Fatalf("edit: %d", code)
	}
	c, row, err := s.manager.LoadCluster(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Spec.Nodes) != 2 {
		t.Errorf("the operation's node was lost: %+v", c.Spec.Nodes)
	}
	if len(c.Spec.Network.NTP) != 1 || c.Spec.Network.NTP[0] != "time.example" {
		t.Errorf("the edit was lost: %v", c.Spec.Network.NTP)
	}
	if row.State != cluster.StateBootstrapped {
		t.Errorf("an edit must keep the state the operation set, got %s", row.State)
	}
}

func TestEditRefusedWhileAnOperationKeepsTheCluster(t *testing.T) {
	s, _ := editServer(t)
	defer func(d time.Duration) { lockWait = d }(lockWait)
	lockWait = 50 * time.Millisecond
	body, _ := json.Marshal(formOf(t, s))
	s.locks.lockContext(t.Context(), specLock("c"))
	defer s.locks.unlock(specLock("c"))
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)); rec.Code != http.StatusConflict {
		t.Errorf("form: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "DELETE", "/api/v1/clusters/c", ""); rec.Code != http.StatusConflict {
		t.Errorf("forget: %d %s", rec.Code, rec.Body)
	}
}

func TestEditLockFollowsWhetherTheOperationWritesTheSpec(t *testing.T) {
	s, st := editServer(t)
	defer func(d time.Duration) { lockWait = d }(lockWait)
	lockWait = 200 * time.Millisecond
	body, _ := json.Marshal(formOf(t, s))
	for _, c := range []struct {
		kind string
		want int
	}{
		{"etcd.snapshot", http.StatusOK},
		{"platform.plan", http.StatusOK},
		{"node.reboot", http.StatusOK},
		{"node.rename", http.StatusConflict},
		{"upgrade.talos", http.StatusConflict},
		{"cluster.apply", http.StatusConflict},
	} {
		t.Run(c.kind, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			id, err := s.runOperation("c", c.kind, nil, func(context.Context, cluster.Sink) (any, error) {
				close(started)
				<-release
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			<-started
			if rec := call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)); rec.Code != c.want {
				t.Errorf("edit during %s: %d %s", c.kind, rec.Code, rec.Body)
			}
			if rec := call(t, s, "DELETE", "/api/v1/clusters/c", ""); rec.Code != http.StatusConflict {
				t.Errorf("forget during %s: %d %s", c.kind, rec.Code, rec.Body)
			}
			close(release)
			waitOp(t, st, id)
		})
	}
}

func TestEditValidationAndNameCheck(t *testing.T) {
	s, _ := editServer(t)
	f := formOf(t, s)
	f.PodCIDR = "not a cidr"
	body, _ := json.Marshal(f)
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("invalid form: %d %s", rec.Code, rec.Body)
	}
	other := "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: d}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n"
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/yaml", other); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `declaration names cluster \"d\"`) {
		t.Errorf("renamed declaration: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/addons/nope", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown add-on: %d %s", rec.Code, rec.Body)
	}
	var buf bytes.Buffer
	buf.WriteString("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n  network: {ntp: [pool.example]}\n")
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/yaml", buf.String()); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pool.example") {
		t.Errorf("yaml save: %d %s", rec.Code, rec.Body)
	}
	dropped := strings.Replace(buf.String(), "hostname: a, ip: 10.0.0.1", "hostname: b, ip: 10.0.0.2", 1)
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/yaml", dropped); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "a is missing") {
		t.Errorf("a node dropped from the YAML: %d %s", rec.Code, rec.Body)
	}
}

func TestUnreadablePlanIsNotApplied(t *testing.T) {
	s, st := editServer(t)
	ctx := t.Context()
	id, err := st.CreateOperation(ctx, "c", "platform.plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOperationArtifact(ctx, id, []byte(`{"timestamp": 5}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishOperation(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, "POST", "/api/v1/clusters/c/platform/apply/"+strconv.FormatInt(id, 10), "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "plan again") {
		t.Errorf("unreadable plan: %d %s", rec.Code, rec.Body)
	}
}

func TestMaintenanceStatusTellsWhenTheWindowCloses(t *testing.T) {
	s, _ := editServer(t)
	now := time.Now().UTC()
	window := "daily " + now.Add(-time.Hour).Format("15:04") + "-" + now.Add(time.Hour).Format("15:04")
	c, _, err := s.manager.LoadCluster(t.Context(), "c")
	if err != nil {
		t.Fatal(err)
	}
	c.Spec.Maintenance = config.Maintenance{Window: window, Timezone: "UTC"}
	if err := s.manager.SaveCluster(t.Context(), c, ""); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, "GET", "/api/v1/clusters/c/maintenance", "")
	var out struct {
		Open   bool   `json:"open"`
		Closes string `json:"closes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	closes, err := time.Parse(time.RFC3339, out.Closes)
	if !out.Open || err != nil || closes.Sub(now) < 58*time.Minute || closes.Sub(now) > time.Hour {
		t.Errorf("open window: %s", rec.Body)
	}
}

func TestEditSaveErrors(t *testing.T) {
	s, st := editServer(t)
	ctx := t.Context()
	if err := st.UpsertNode(ctx, store.NodeRow{MAC: "52:54:00:00:00:01", IP: "10.0.0.1", Source: "scan", Cluster: "c", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeMachineConfig(ctx, "10.0.0.1", []byte("cfg"), true); err != nil {
		t.Fatal(err)
	}
	yaml := "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: c}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n  storage: {ephemeralSize: 20GiB}\n"
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/yaml", yaml); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "storage must stay") {
		t.Errorf("a refused spec change: %d %s", rec.Code, rec.Body)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.manager.Home, "kubit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER refuse BEFORE UPDATE ON clusters BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(formOf(t, s))
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)); rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("a failed write: %d %s", rec.Code, rec.Body)
	}
}
