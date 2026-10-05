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
	if !ProbeRedfish(ctx, srv.URL, 2*time.Second) {
		t.Error("service root probe failed")
	}
	if ProbeRedfish(ctx, "https://127.0.0.1:1", time.Second) {
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

func TestRedfishClientsShareOneTransport(t *testing.T) {
	a, b := newRedfish(Config{Host: "10.0.0.1"}, nil), newRedfish(Config{Host: "10.0.0.2"}, nil)
	if a.client.Transport != b.client.Transport || a.client == b.client || a.writes.Transport != b.writes.Transport {
		t.Error("each BMC client must reuse the package transport, not open its own pool")
	}
}

func TestAliveChecksLoginWithOneRequest(t *testing.T) {
	bmc := &fakeBMC{power: "On", user: "root", pass: "calvin"}
	var requests int
	h := bmc.handler()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx := context.Background()
	if err := Alive(ctx, Config{Type: "redfish", Host: srv.URL, User: "root", Password: "calvin"}); err != nil || requests != 1 {
		t.Errorf("alive: %v after %d requests", err, requests)
	}
	if err := Alive(ctx, Config{Type: "redfish", Host: srv.URL, User: "root", Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("a wrong password is not alive: %v", err)
	}
	if err := Alive(ctx, Config{Type: "amt", Host: "x"}); err == nil {
		t.Error("missing credentials are refused before any request")
	}
	if redfishTransport.Proxy != nil {
		t.Error("a LAN BMC is never reached through a proxy")
	}
}

func iLO4(t *testing.T, oem map[string]any, adapters bool) (*httptest.Server, *[]string) {
	t.Helper()
	var resets []string
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"RedfishVersion": "1.0.0", "Systems": map[string]string{"@odata.id": "/redfish/v1/Systems/"}})
	})
	mux.HandleFunc("/redfish/v1/Systems/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/1/"}}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusOK)
			return
		}
		write(w, map[string]any{
			"Manufacturer": "HP", "Model": "ProLiant ML350p Gen8", "SerialNumber": "CZ1234567", "PowerState": "On",
			"Boot":               map[string]any{"BootSourceOverrideSupported": []string{"None", "Cd", "Hdd", "Usb", "Pxe"}},
			"EthernetInterfaces": map[string]string{"@odata.id": "/redfish/v1/Systems/1/EthernetInterfaces/"},
			"Oem":                oem,
			"Actions":            map[string]any{"#ComputerSystem.Reset": map[string]any{"target": "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/", "ResetType@Redfish.AllowableValues": []string{"On", "ForceOff", "ForceRestart", "Nmi", "PushPowerButton"}}},
		})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/EthernetInterfaces/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"Members": []map[string]string{}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/NetworkAdapters/", func(w http.ResponseWriter, r *http.Request) {
		if !adapters {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		write(w, map[string]any{"Members": []map[string]string{{"@odata.id": "/redfish/v1/Systems/1/NetworkAdapters/1/"}}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/NetworkAdapters/1/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"PhysicalPorts": []map[string]string{{"MacAddress": ""}, {"MacAddress": "AC:16:2D:11:22:33"}, {"MacAddress": "ac:16:2d:11:22:34"}}})
	})
	mux.HandleFunc("/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ ResetType string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		resets = append(resets, body.ResetType)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, &resets
}

func TestRedfishFindsTheHostMACOnAnILO4(t *testing.T) {
	links := map[string]any{"NetworkAdapters": map[string]string{"@odata.id": "/redfish/v1/Systems/1/NetworkAdapters/"}}
	for name, c := range map[string]struct {
		oem      map[string]any
		adapters bool
	}{
		"host correlation": {map[string]any{"Hp": map[string]any{"HostCorrelation": map[string]any{"HostMACAddress": []string{"00:00:00:00:00:00", "ac:16:2d:11:22:33", "ac:16:2d:11:22:34"}}, "Links": links}}, false},
		"network adapters": {map[string]any{"Hp": map[string]any{"Links": links}}, true},
	} {
		srv, resets := iLO4(t, c.oem, c.adapters)
		m, err := Open(Config{Type: "redfish", Host: srv.URL, User: "admin", Password: "pw"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		info, err := m.Probe(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.MAC != "ac:16:2d:11:22:33" || info.Model != "ProLiant ML350p Gen8" {
			t.Errorf("%s: probe %+v", name, info)
		}
		if err := m.Power(ctx, BootPXE); err != nil {
			t.Fatalf("%s: pxe: %v", name, err)
		}
		if len(*resets) != 1 || (*resets)[0] != "ForceRestart" {
			t.Errorf("%s: a running iLO 4 host restarts into PXE, got %v", name, *resets)
		}
		cancel()
	}
}

func TestRedfishWritesSurviveABMCThatDropsTheConnection(t *testing.T) {
	var resets []string
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RedfishVersion":"1.0.0","Systems":{"@odata.id":"/redfish/v1/Systems/"}}`))
	})
	mux.HandleFunc("/redfish/v1/Systems/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Members":[{"@odata.id":"/redfish/v1/Systems/1/"}]}`))
	})
	mux.HandleFunc("/redfish/v1/Systems/1/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			_, _ = w.Write([]byte(`{"PowerState":"Off","Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/"}}}`))
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
		_ = buf.Flush()
		go func() { time.Sleep(300 * time.Millisecond); conn.Close() }()
	})
	mux.HandleFunc("/redfish/v1/Systems/1/Actions/ComputerSystem.Reset/", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ ResetType string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		resets = append(resets, body.ResetType)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	m, err := Open(Config{Type: "redfish", Host: srv.URL, User: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Power(ctx, BootPXE); err != nil {
		t.Fatalf("an iLO that drops the connection after a write must not fail the next write: %v", err)
	}
	if len(resets) != 1 || resets[0] != "On" {
		t.Errorf("resets: %v", resets)
	}
}
