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
	v.PXEEnrollment = "open"
	_ = s.PutSettings(ctx, v)
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.7", MAC: "aa:aa:aa:aa:aa:07", State: "labhost"})
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:07", &store.LabHost{State: "ready"})
	if decide("aa:aa:aa:aa:aa:07") != "local" {
		t.Error("an installed lab host boots its own disk")
	}
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:07", &store.LabHost{State: "error", Error: "x"})
	if decide("aa:aa:aa:aa:aa:07") != "talos" {
		t.Error("a failed lab host is an ordinary known machine again")
	}
	// Armed and mid-install → the Debian installer.
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:07", &store.LabHost{State: "installing"})
	_ = s.SetMachineProvision(ctx, "aa:aa:aa:aa:aa:07", true, "labhost")
	if decide("aa:aa:aa:aa:aa:07") != "debian" {
		t.Error("a lab host mid-install gets the Debian installer")
	}
	// S1 regression: a READY lab host still carrying a stale arm must NOT be re-imaged.
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:07", &store.LabHost{State: "ready"})
	if decide("aa:aa:aa:aa:aa:07") != "local" {
		t.Error("a ready lab host must boot its own disk even if still armed")
	}
	// Discovery seeing it in maintenance mode clears the arming.
	_ = s.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.6", MAC: "aa:aa:aa:aa:aa:06", Source: "scan", State: "maintenance"})
	m, _ := s.GetMachine(ctx, "aa:aa:aa:aa:aa:06")
	if m.Provision || m.Cluster != "" {
		t.Errorf("maintenance sighting should clear provision and membership: %+v", m)
	}
}
