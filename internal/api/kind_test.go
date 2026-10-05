package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestNodeEndpointsRefuseNonTalosKinds(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := context.Background()
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:12", Source: "pxe", State: "unknown"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:13", IP: "10.0.0.13", Source: "scan", State: "configured"})
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:14", IP: "10.0.0.14", Source: "scan", State: "offline"})
	cases := map[string]string{
		"aa:aa:aa:aa:aa:12": "No address is known",
		"10.0.0.13":         "Configured outside Kubit",
		"10.0.0.14":         "Not answering",
	}
	for addr, want := range cases {
		for _, ep := range []struct{ method, path string }{{"GET", "/inventory"}, {"GET", "/services"}, {"GET", "/logs"}} {
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
	_ = s.UpsertNode(ctx, store.NodeRow{MAC: "aa:aa:aa:aa:aa:31", IP: "10.0.0.31", Source: "scan", State: "configured"})
	var all []struct {
		MAC   string `json:"mac"`
		Kind  string `json:"kind"`
		Talos bool   `json:"talos"`
	}
	rec := do(t, srv, "GET", "/api/v1/nodes", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	kinds := map[string]string{}
	for _, m := range all {
		kinds[m.MAC] = m.Kind
		if m.MAC == "aa:aa:aa:aa:aa:30" && !m.Talos {
			t.Error("maintenance machine has a Talos API")
		}
	}
	if kinds["aa:aa:aa:aa:aa:30"] != "maintenance" || kinds["aa:aa:aa:aa:aa:31"] != "configured" {
		t.Errorf("kinds: %v", kinds)
	}
}
