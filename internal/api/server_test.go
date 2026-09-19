package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/api"
	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/store"
)

func newServer(t *testing.T, token string) (*api.Server, *store.Store) {
	t.Helper()
	c, _ := store.NewCrypto(bytes.Repeat([]byte{3}, 32))
	dir := t.TempDir()
	s, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return api.New("test", cluster.NewManager(s, dir), token, c), s
}

func do(t *testing.T, h http.Handler, method, path string, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:40000"
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTokenGuardsAPIOnly(t *testing.T) {
	srv, _ := newServer(t, "secret")
	if rec := do(t, srv, "GET", "/api/v1/clusters", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Authorization", "Bearer secret"); rec.Code != http.StatusOK {
		t.Errorf("with token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/version", ""); rec.Code != http.StatusOK {
		t.Errorf("version is open so the UI can greet before sign-in: %d", rec.Code)
	}
	// The browser WebSocket cannot set headers: the token may come as a query
	// parameter. Without it the upgrade is refused before any handshake.
	if rec := do(t, srv, "GET", "/api/v1/ws", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("ws without token: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/ws?token=secret", ""); rec.Code == http.StatusUnauthorized {
		t.Errorf("ws with query token must pass the guard, got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/", ""); rec.Code != http.StatusOK {
		t.Errorf("SPA must not require the token: %d", rec.Code)
	}
}

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true, "0.0.0.0:8080": false, "192.168.1.5:8080": false, "bad": false} {
		if got := api.Loopback(addr); got != want {
			t.Errorf("Loopback(%q) = %v", addr, got)
		}
	}
}

func TestConfigValidateAndDraft(t *testing.T) {
	srv, s := newServer(t, "")
	rec := do(t, srv, "POST", "/api/v1/config/validate", "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: x}\nspec:\n  nodes: []\n")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty nodes should be 422, got %d: %s", rec.Code, rec.Body)
	}
	rec = do(t, srv, "POST", "/api/v1/config/validate", "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: x}\nspec:\n  nodes:\n    - {hostname: a, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "talosVersion") {
		t.Errorf("valid: %d %s", rec.Code, rec.Body)
	}

	for _, n := range []store.NodeRow{
		{IP: "10.0.0.1", MAC: "aa:aa:aa:aa:aa:aa", Arch: "amd64", State: "maintenance", Hardware: []byte(`{"kvm":true,"disks":[{"devPath":"/dev/nvme0n1","sizeBytes":500000000000,"transport":"nvme"}]}`)},
		{IP: "10.0.0.2", MAC: "bb:bb:bb:bb:bb:bb", Arch: "amd64", State: "maintenance"},
		{IP: "10.0.0.3", MAC: "cc:cc:cc:cc:cc:cc", Arch: "amd64", State: "maintenance"},
	} {
		if err := s.UpsertNode(t.Context(), n); err != nil {
			t.Fatal(err)
		}
	}
	rec = do(t, srv, "POST", "/api/v1/config/draft", `{"name":"lab","ips":["10.0.0.1","10.0.0.2","10.0.0.3"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		YAML     string `json:"yaml"`
		Topology struct{ ControlPlanes, Workers int }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Topology.ControlPlanes != 3 || out.Topology.Workers != 0 {
		t.Errorf("3 nodes should draft 3 control planes: %+v", out.Topology)
	}
	for _, want := range []string{"lab-cp-01", "lab-cp-03", "/dev/nvme0n1", "kvm: true", "10.0.0.200-10.0.0.220", "mac: aa:aa:aa:aa:aa:aa"} {
		if !strings.Contains(out.YAML, want) {
			t.Errorf("draft lacks %q:\n%s", want, out.YAML)
		}
	}
}

func TestClusterCreateRejectsBadYAML(t *testing.T) {
	srv, _ := newServer(t, "")
	rec := do(t, srv, "POST", "/api/v1/clusters", `{"yaml":"kind: Nope"}`)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("clusters: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv, "GET", "/api/v1/operations", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("operations: %s", rec.Body)
	}
}

func TestDesignAndLint(t *testing.T) {
	srv, s := newServer(t, "")
	hw := `{"cpus":4,"memoryBytes":8589934592,"kvm":false,"disks":[{"devPath":"/dev/sda","sizeBytes":250000000000}]}`
	for i, mac := range []string{"aa:aa:aa:aa:aa:01", "aa:aa:aa:aa:aa:02", "aa:aa:aa:aa:aa:03"} {
		if err := s.UpsertNode(t.Context(), store.NodeRow{IP: "10.0.0.1" + string(rune('0'+i)), MAC: mac, Arch: "amd64", State: "maintenance", Hardware: []byte(hw)}); err != nil {
			t.Fatal(err)
		}
	}
	rec := do(t, srv, "POST", "/api/v1/config/design", `{"name":"lab","macs":["aa:aa:aa:aa:aa:01","aa:aa:aa:aa:aa:02","aa:aa:aa:aa:aa:03"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("design: %d %s", rec.Code, rec.Body)
	}
	var d struct {
		YAML     string `json:"yaml"`
		Topology struct{ ControlPlanes int }
		Warnings []struct{ Code string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d.Topology.ControlPlanes != 3 || !strings.Contains(d.YAML, "pool: controlplane") || !strings.Contains(d.YAML, "vip: 10.0.0.250") {
		t.Errorf("design: %+v\n%s", d.Topology, d.YAML)
	}
	body, _ := json.Marshal(map[string]string{"yaml": strings.Replace(d.YAML, "vip: 10.0.0.250", "", 1)})
	rec = do(t, srv, "POST", "/api/v1/config/lint", string(body))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "no-vip") {
		t.Errorf("lint should flag the missing VIP: %d %s", rec.Code, rec.Body)
	}
}

func TestMachinesAndRetire(t *testing.T) {
	srv, s := newServer(t, "")
	_ = s.UpsertNode(t.Context(), store.NodeRow{IP: "10.0.0.5", MAC: "aa:aa:aa:aa:aa:05", State: "maintenance"})
	_ = s.PutCluster(t.Context(), store.ClusterRow{Name: "c", Spec: []byte("x")})
	_ = s.UpsertNode(t.Context(), store.NodeRow{IP: "10.0.0.6", MAC: "aa:aa:aa:aa:aa:06", Cluster: "c", Hostname: "n", State: "ready"})
	if rec := do(t, srv, "GET", "/api/v1/machines/aa:aa:aa:aa:aa:05", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ip":"10.0.0.5"`) {
		t.Errorf("machine: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv, "DELETE", "/api/v1/machines/aa:aa:aa:aa:aa:06", ""); rec.Code != http.StatusConflict {
		t.Errorf("retiring a member must be refused: %d", rec.Code)
	}
	if rec := do(t, srv, "DELETE", "/api/v1/machines/aa:aa:aa:aa:aa:05", ""); rec.Code != http.StatusNoContent {
		t.Errorf("retire: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, srv, "POST", "/api/v1/machines/aa:aa:aa:aa:aa:06/wake", ""); rec.Code != http.StatusConflict {
		t.Errorf("wake without WOL enabled must be refused: %d", rec.Code)
	}
}
