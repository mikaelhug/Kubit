package api

import (
	"bytes"
	"encoding/json"
	"net/http"
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

	s.locks.lock("c")
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
	s.locks.unlock("c")
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
	s.locks.lock("c")
	defer s.locks.unlock("c")
	if rec := call(t, s, "PUT", "/api/v1/clusters/c/form", string(body)); rec.Code != http.StatusConflict {
		t.Errorf("form: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "DELETE", "/api/v1/clusters/c", ""); rec.Code != http.StatusConflict {
		t.Errorf("forget: %d %s", rec.Code, rec.Body)
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
