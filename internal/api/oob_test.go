package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestPXEDecision(t *testing.T) {
	srv, s := newServer(t, "")
	ctx := t.Context()
	if err := s.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.5", MAC: "aa:aa:aa:aa:aa:05", State: "maintenance"})
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.6", MAC: "aa:aa:aa:aa:aa:06", Cluster: "c", Hostname: "n", State: "ready"})
	decide := func(mac string) string {
		rec := do(t, srv, "GET", "/api/v1/pxe/decide?mac="+mac, "")
		var d struct{ Boot string }
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		if rec.Code != http.StatusOK {
			t.Fatalf("decide %s: %d", mac, rec.Code)
		}
		return d.Boot
	}
	if decide("aa:aa:aa:aa:aa:05") != "talos" {
		t.Error("known unassigned machine should get talos")
	}
	if decide("aa:aa:aa:aa:aa:06") != "local" {
		t.Error("cluster member should boot locally")
	}
	if decide("aa:aa:aa:aa:aa:99") != "talos" {
		t.Error("unknown machine gets talos while enrollment is open")
	}
	v, _ := s.GetSettings(ctx)
	v.PXEEnrollment = "closed"
	_ = s.PutSettings(ctx, v)
	if decide("aa:aa:aa:aa:aa:99") != "local" {
		t.Error("unknown machine boots locally when enrollment is closed")
	}
	_ = s.SetMachineProvision(ctx, "aa:aa:aa:aa:aa:06", true)
	if decide("aa:aa:aa:aa:aa:06") != "talos" {
		t.Error("an armed member gets talos once")
	}
	// Discovery seeing it in maintenance mode clears the arming.
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.6", MAC: "aa:aa:aa:aa:aa:06", Source: "scan", State: "maintenance"})
	m, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:06")
	if m.Provision || m.Cluster != "" {
		t.Errorf("maintenance sighting should clear provision and membership: %+v", m)
	}
}
