package oob

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBMC struct {
	mu     sync.Mutex
	power  string
	boot   map[string]string
	resets []string
	user   string
	pass   string
}

func (f *fakeBMC) handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if u, p, ok := r.BasicAuth(); !ok || u != f.user || p != f.pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/redfish/v1", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"RedfishVersion": "1.15.0", "Systems": map[string]string{"@odata.id": "/redfish/v1/Systems"}})
	})
	mux.HandleFunc("/redfish/v1/Systems", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/System.Embedded.1"}}})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPatch {
			var body struct{ Boot map[string]string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.boot = body.Boot
			w.WriteHeader(http.StatusNoContent)
			return
		}
		write(w, map[string]any{
			"Id": "System.Embedded.1", "Manufacturer": "Dell Inc.", "Model": "PowerEdge R650", "SerialNumber": "ABC1234", "UUID": "4C4C4544-0000-1010-8080-B4C04F313233",
			"PowerState":         f.power,
			"Boot":               map[string]any{"BootSourceOverrideEnabled": "Disabled", "BootSourceOverrideTarget": "None", "BootSourceOverrideTarget@Redfish.AllowableValues": []string{"None", "Pxe", "Hdd"}},
			"MemorySummary":      map[string]any{"TotalSystemMemoryGiB": 128},
			"ProcessorSummary":   map[string]any{"Count": 2},
			"EthernetInterfaces": map[string]string{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces"},
			"Storage":            map[string]string{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/Storage"},
			"Actions":            map[string]any{"#ComputerSystem.Reset": map[string]any{"target": "/redfish/v1/Systems/System.Embedded.1/Actions/ComputerSystem.Reset", "ResetType@Redfish.AllowableValues": []string{"On", "ForceOff", "GracefulRestart", "PowerCycle"}}},
		})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/Actions/ComputerSystem.Reset", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ ResetType string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.resets = append(f.resets, body.ResetType)
		if body.ResetType == "On" {
			f.power = "On"
		}
		f.mu.Unlock()
		if body.ResetType == "Nmi" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad","@Message.ExtendedInfo":[{"Message":"The value Nmi is not allowed"}]}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces/NIC.1"}, {"@odata.id": "/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces/NIC.2"}}})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces/NIC.1", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"MACAddress": "", "PermanentMACAddress": ""})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/EthernetInterfaces/NIC.2", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"MACAddress": "B4-C0-4F-31-32-33", "PermanentMACAddress": "B4-C0-4F-31-32-33"})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/Storage", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/Storage/RAID.1"}}})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/Storage/RAID.1", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Drives": []map[string]string{{"@odata.id": "/redfish/v1/Systems/System.Embedded.1/Storage/RAID.1/Drives/Disk.0"}}})
	}))
	mux.HandleFunc("/redfish/v1/Systems/System.Embedded.1/Storage/RAID.1/Drives/Disk.0", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Model": "SAMSUNG PM9A3", "SerialNumber": "S1", "CapacityBytes": 960197124096, "MediaType": "SSD", "Protocol": "NVMe"})
	}))
	return mux
}

func TestRedfishProbeAndPower(t *testing.T) {
	bmc := &fakeBMC{power: "Off", user: "root", pass: "calvin"}
	srv := httptest.NewTLSServer(bmc.handler())
	defer srv.Close()
	var lines []string
	m, err := Open(Config{Type: "redfish", Host: srv.URL, User: "root", Password: "calvin"}, WithTrace(func(l string) { lines = append(lines, l) }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := m.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "Redfish 1.15.0" || info.MAC != "b4:c0:4f:31:32:33" || info.Model != "PowerEdge R650" || info.Serial != "ABC1234" || info.Power != "off" || info.CPUs != 2 || info.MemoryBytes != 128<<30 || info.UUID != "4c4c4544-0000-1010-8080-b4c04f313233" {
		t.Errorf("probe: %+v", info)
	}
	if len(info.Disks) != 1 || info.Disks[0].Transport != "nvme" || info.Disks[0].Media != "ssd" || info.Disks[0].SizeBytes != 960197124096 {
		t.Errorf("disks: %+v", info.Disks)
	}
	if err := m.Power(ctx, BootPXE); err != nil {
		t.Fatal(err)
	}
	if bmc.boot["BootSourceOverrideEnabled"] != "Once" || bmc.boot["BootSourceOverrideTarget"] != "Pxe" || len(bmc.resets) != 1 || bmc.resets[0] != "On" {
		t.Errorf("pxe from off: boot %v resets %v", bmc.boot, bmc.resets)
	}
	if err := m.Power(ctx, Reset); err != nil {
		t.Fatal(err)
	}
	if bmc.resets[1] != "GracefulRestart" {
		t.Errorf("ForceRestart must fall back to what the BMC allows, got %s", bmc.resets[1])
	}
	if err := m.Power(ctx, BootPXE); err != nil {
		t.Fatal(err)
	}
	if bmc.resets[2] != "GracefulRestart" {
		t.Errorf("pxe on a running box restarts it, got %s", bmc.resets[2])
	}
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "Once") {
		t.Errorf("trace must show the boot override: %v", lines)
	}
}

func TestRedfishErrors(t *testing.T) {
	bmc := &fakeBMC{power: "On", user: "root", pass: "calvin"}
	srv := httptest.NewTLSServer(bmc.handler())
	defer srv.Close()
	ctx := context.Background()
	m, _ := Open(Config{Type: "redfish", Host: srv.URL, User: "root", Password: "wrong"})
	if _, err := m.Probe(ctx); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("bad password: %v", err)
	}
	if _, err := Open(Config{Type: "redfish", Host: srv.URL}); err == nil {
		t.Error("missing credentials must be refused")
	}
	if v, ok := ProbeRedfish(ctx, srv.URL, 2*time.Second); !ok || v != "1.15.0" {
		t.Errorf("service root probe: %q %v", v, ok)
	}
	if _, ok := ProbeRedfish(ctx, "https://127.0.0.1:1", time.Second); ok {
		t.Error("closed port must not look like a BMC")
	}
	m, _ = Open(Config{Type: "redfish", Host: srv.URL, User: "root", Password: "calvin"})
	if err := m.Power(ctx, Action("nmi")); err == nil {
		t.Error("unknown action must be refused")
	}
	if got := redfishMessage(400, []byte(`{"error":{"message":"bad","@Message.ExtendedInfo":[{"Message":"The value Nmi is not allowed"}]}}`)); got != "400 The value Nmi is not allowed" {
		t.Errorf("message: %q", got)
	}
}
