package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if decide("aa:aa:aa:aa:aa:07") != "local" {
		t.Error("a failed lab host keeps whatever is on its disk")
	}
	_ = s.SetLabHost(ctx, "aa:aa:aa:aa:aa:07", &store.LabHost{State: "setup"})
	_ = s.SetMachineProvision(ctx, "aa:aa:aa:aa:aa:07", true, "labhost")
	if decide("aa:aa:aa:aa:aa:07") != "local" {
		t.Error("a lab host in setup has Debian installed; a stray network boot must not re-image it")
	}
	_ = s.SetMachineProvision(ctx, "aa:aa:aa:aa:aa:07", false)
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

func fakeRedfish() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/redfish/v1", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"RedfishVersion": "1.11.0", "Systems": map[string]string{"@odata.id": "/redfish/v1/Systems"}})
	})
	mux.HandleFunc("/redfish/v1/Systems", func(w http.ResponseWriter, r *http.Request) {
		if _, p, _ := r.BasicAuth(); p != "calvin" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/1"}}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Manufacturer": "HPE", "Model": "ProLiant DL380 Gen11", "SerialNumber": "CZ123", "UUID": "AABBCCDD-0000-0000-0000-000000000001", "PowerState": "Off",
			"MemorySummary": map[string]any{"TotalSystemMemoryGiB": 64}, "ProcessorSummary": map[string]any{"Count": 1},
			"EthernetInterfaces": map[string]string{"@odata.id": "/redfish/v1/Systems/1/EthernetInterfaces"}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/EthernetInterfaces", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/1/EthernetInterfaces/1"}}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/EthernetInterfaces/1", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"MACAddress": "94:40:C9:11:22:33"})
	})
	return mux
}

func TestOOBAddRedfish(t *testing.T) {
	bmc := httptest.NewTLSServer(fakeRedfish())
	defer bmc.Close()
	srv, s := newServer(t, "")
	rec := do(t, srv, "POST", "/api/v1/machines/oob", `{"type":"redfish","host":"`+bmc.URL+`","user":"root","password":"nope"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "authentication failed") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, srv, "POST", "/api/v1/machines/oob", `{"type":"redfish","host":"`+bmc.URL+`","user":"root","password":"calvin"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	m, err := s.GetMachine(t.Context(), "94:40:c9:11:22:33")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != "redfish" || m.State != "off" || m.Serial != "CZ123" || m.UUID != "aabbccdd-0000-0000-0000-000000000001" || m.OOBType != "redfish" || m.IP != "" {
		t.Errorf("row: %+v", m)
	}
	var hw struct {
		Product     string `json:"product"`
		CPUs        int    `json:"cpus"`
		MemoryBytes int64  `json:"memoryBytes"`
	}
	_ = json.Unmarshal(m.Hardware, &hw)
	if hw.Product != "ProLiant DL380 Gen11" || hw.CPUs != 1 || hw.MemoryBytes != 64<<30 {
		t.Errorf("hardware stand-in: %s", m.Hardware)
	}
	rec = do(t, srv, "POST", "/api/v1/machines/94:40:c9:11:22:33/oob/test", `{}`)
	var res struct {
		OK   bool
		Info struct{ Version, Power string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || !res.OK || res.Info.Version != "Redfish 1.11.0" || res.Info.Power != "off" {
		t.Errorf("test: %d %s", rec.Code, rec.Body.String())
	}
}
