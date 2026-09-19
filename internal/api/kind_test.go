package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/store"
)

func TestNodeEndpointsRefuseNonTalosKinds(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	host := "aa:aa:aa:aa:aa:10"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: host, IP: "10.0.0.10", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "ready"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:11", IP: "10.0.0.11", Source: "lab", State: "off"})
	_ = s.SetMachineHost(ctx, "aa:aa:aa:aa:aa:11", host)
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:12", Source: "amt", State: "unknown"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:13", IP: "10.0.0.13", Source: "scan", State: "configured"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:14", IP: "10.0.0.14", Source: "amt", State: "amt", Provision: true})
	_ = s.SetMachineProvision(ctx, "aa:aa:aa:aa:aa:14", true, "talos")
	cases := map[string]string{
		"10.0.0.10":         "lab host running Debian",
		"10.0.0.11":         "The VM is off",
		"aa:aa:aa:aa:aa:12": "No address is known",
		"10.0.0.13":         "configured outside Kubit",
		"10.0.0.14":         "Waiting for Talos",
	}
	for addr, want := range cases {
		for _, ep := range []struct{ method, path string }{{"GET", "/inventory"}, {"GET", "/services"}, {"GET", "/logs"}, {"POST", "/reboot"}} {
			rec := do(t, srv, ep.method, "/api/v1/nodes/"+addr+ep.path, "")
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s %s%s: %d %s", ep.method, addr, ep.path, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestNodeEndpointsReportClosedPort(t *testing.T) {
	srv, s := newServer(t, "")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	l.Close()
	_ = s.UpsertNode(context.Background(), store.NodeRow{MAC: "aa:aa:aa:aa:aa:20", IP: "127.0.0.1", Source: "scan", State: "maintenance"})
	rec := do(t, srv, "GET", "/api/v1/nodes/127.0.0.1/inventory", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "not answering") {
		t.Errorf("closed port: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMachineJSONCarriesKind(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:30", IP: "10.0.0.30", Source: "scan", State: "maintenance"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:31", IP: "10.0.0.31", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:31", &store.LabHost{State: "installing"})
	var one struct {
		Kind  string `json:"kind"`
		Talos bool   `json:"talos"`
	}
	rec := do(t, srv, "GET", "/api/v1/machines/aa:aa:aa:aa:aa:31", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &one)
	if rec.Code != http.StatusOK || one.Kind != "labhost" || one.Talos {
		t.Errorf("machine: %d %s", rec.Code, rec.Body.String())
	}
	var all []struct {
		MAC   string `json:"mac"`
		Kind  string `json:"kind"`
		Talos bool   `json:"talos"`
	}
	rec = do(t, srv, "GET", "/api/v1/machines", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	kinds := map[string]string{}
	for _, m := range all {
		kinds[m.MAC] = m.Kind
		if m.MAC == "aa:aa:aa:aa:aa:30" && !m.Talos {
			t.Error("maintenance machine has a Talos API")
		}
	}
	if kinds["aa:aa:aa:aa:aa:30"] != "maintenance" || kinds["aa:aa:aa:aa:aa:31"] != "labhost" {
		t.Errorf("kinds: %v", kinds)
	}
}

func TestPowerPXERefusesLabHostAndVM(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	host := "aa:aa:aa:aa:aa:40"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: host, IP: "10.0.0.40", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "ready"})
	_ = s.SetMachineOOB(ctx, host, &oob.Config{Type: "amt", Host: "10.0.0.40", User: "admin", Password: "x"})
	vm := "aa:aa:aa:aa:aa:41"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: vm, IP: "10.0.0.41", Source: "lab", State: "off"})
	_ = s.SetMachineHost(ctx, vm, host)
	_ = s.SetMachineOOB(ctx, vm, &oob.Config{Type: "amt", Host: "10.0.0.41", User: "admin", Password: "x"})
	v, _ := s.GetSettings(ctx)
	v.PXEStatusURL = ""
	_ = s.PutSettings(ctx, v)
	for mac, want := range map[string]string{host: "release it first", vm: "re-provisioned from their host"} {
		rec := do(t, srv, "POST", "/api/v1/machines/"+mac+"/power", `{"action":"pxe"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("pxe on %s: %d %s", mac, rec.Code, rec.Body.String())
		}
	}
}

func TestLabProvisionRefusesVMAndExistingHost(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	host := "aa:aa:aa:aa:aa:50"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: host, IP: "10.0.0.50", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "ready"})
	vm := "aa:aa:aa:aa:aa:51"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: vm, IP: "10.0.0.51", Source: "lab", State: "maintenance"})
	_ = s.SetMachineHost(ctx, vm, host)
	if rec := do(t, srv, "POST", "/api/v1/machines/"+vm+"/labhost", `{"manual":true}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cannot host VMs") {
		t.Errorf("vm: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "POST", "/api/v1/machines/"+host+"/labhost", `{"manual":true}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Already a lab host") {
		t.Errorf("host: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLabReleaseGuardsAndState(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	host := "aa:aa:aa:aa:aa:60"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: host, IP: "10.0.0.60", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "installing"})
	if rec := do(t, srv, "DELETE", "/api/v1/machines/"+host+"/labhost", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "installing") {
		t.Errorf("release while installing: %d %s", rec.Code, rec.Body.String())
	}
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "error"})
	if rec := do(t, srv, "DELETE", "/api/v1/machines/"+host+"/labhost", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("release: %d %s", rec.Code, rec.Body.String())
	}
	m, _ := s.GetMachine(ctx, host)
	if m.LabHost != nil || m.State != "unknown" || m.Kind() != store.KindUnbooted {
		t.Errorf("after release: state %s kind %s labhost %v", m.State, m.Kind(), m.LabHost)
	}
}

func TestRetireRefusesLabHostAndVM(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	host := "aa:aa:aa:aa:aa:70"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: host, IP: "10.0.0.70", Source: "labhost", State: "labhost"})
	_ = s.SetLabHost(ctx, host, &store.LabHost{State: "ready"})
	vm := "aa:aa:aa:aa:aa:71"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: vm, IP: "10.0.0.71", Source: "lab", State: "off"})
	_ = s.SetMachineHost(ctx, vm, host)
	orphan := "aa:aa:aa:aa:aa:72"
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: orphan, IP: "10.0.0.72", Source: "lab", State: "off"})
	_ = s.SetMachineHost(ctx, orphan, "aa:aa:aa:aa:aa:ff")
	if rec := do(t, srv, "DELETE", "/api/v1/machines/"+host, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Release the lab host") {
		t.Errorf("retire host: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "DELETE", "/api/v1/machines/"+vm, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "from its lab host") {
		t.Errorf("retire vm: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "DELETE", "/api/v1/machines/"+orphan, ""); rec.Code != http.StatusNoContent {
		t.Errorf("retire orphan vm: %d %s", rec.Code, rec.Body.String())
	}
}
