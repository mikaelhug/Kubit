package oob

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type redfish struct {
	c      Config
	trace  func(string)
	client *http.Client
	system string
}

const redfishRoot = "/redfish/v1"

func newRedfish(c Config, trace func(string)) *redfish {
	return &redfish{c: c, trace: trace, client: &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}}
}

func (r *redfish) tracef(format string, args ...any) {
	if r.trace != nil {
		r.trace(fmt.Sprintf(format, args...))
	}
}

func (r *redfish) url(path string) string {
	host := r.c.Host
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	return strings.TrimRight(host, "/") + path
}

func (r *redfish) do(ctx context.Context, method, path string, body any, out any) error {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(b)
		r.tracef("%s %s %s", method, path, b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.url(path), buf)
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.c.User, r.c.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("BMC at %s: %w", r.c.Host, describeRedfish(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("BMC at %s: authentication failed (check the BMC user/password)", r.c.Host)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("BMC at %s: %s %s: %s", r.c.Host, method, path, redfishMessage(resp.StatusCode, raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("BMC at %s: %s is not Redfish JSON: %w", r.c.Host, path, err)
		}
	}
	return nil
}

func redfishMessage(status int, raw []byte) string {
	var e struct {
		Error struct {
			Message         string `json:"message"`
			ExtendedMessage []struct {
				Message string `json:"Message"`
			} `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil {
		for _, m := range e.Error.ExtendedMessage {
			if m.Message != "" {
				return fmt.Sprintf("%d %s", status, m.Message)
			}
		}
		if e.Error.Message != "" {
			return fmt.Sprintf("%d %s", status, e.Error.Message)
		}
	}
	return http.StatusText(status)
}

func describeRedfish(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("port closed: no Redfish service at that address: %w", err)
	case strings.Contains(s, "no route") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return fmt.Errorf("no answer: wrong address, or the BMC has no standby power: %w", err)
	}
	return err
}

type redfishRef struct {
	ID string `json:"@odata.id"`
}

type redfishSystem struct {
	ID           string `json:"Id"`
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	SerialNumber string `json:"SerialNumber"`
	UUID         string `json:"UUID"`
	PowerState   string `json:"PowerState"`
	Boot         struct {
		Enabled string   `json:"BootSourceOverrideEnabled"`
		Target  string   `json:"BootSourceOverrideTarget"`
		Allowed []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
	} `json:"Boot"`
	Memory struct {
		GiB float64 `json:"TotalSystemMemoryGiB"`
	} `json:"MemorySummary"`
	Processors struct {
		Count int `json:"Count"`
	} `json:"ProcessorSummary"`
	Ethernet redfishRef `json:"EthernetInterfaces"`
	Storage  redfishRef `json:"Storage"`
	Actions  struct {
		Reset struct {
			Target  string   `json:"target"`
			Allowed []string `json:"ResetType@Redfish.AllowableValues"`
		} `json:"#ComputerSystem.Reset"`
	} `json:"Actions"`
}

func (r *redfish) systemPath(ctx context.Context) (string, error) {
	if r.system != "" {
		return r.system, nil
	}
	var root struct {
		Version string     `json:"RedfishVersion"`
		Systems redfishRef `json:"Systems"`
	}
	if err := r.do(ctx, http.MethodGet, redfishRoot, nil, &root); err != nil {
		return "", err
	}
	if root.Systems.ID == "" {
		return "", fmt.Errorf("BMC at %s: the Redfish service lists no Systems", r.c.Host)
	}
	var col struct {
		Members []redfishRef `json:"Members"`
	}
	if err := r.do(ctx, http.MethodGet, root.Systems.ID, nil, &col); err != nil {
		return "", err
	}
	if len(col.Members) == 0 {
		return "", fmt.Errorf("BMC at %s: no ComputerSystem behind the Redfish service", r.c.Host)
	}
	r.system = col.Members[0].ID
	return r.system, nil
}

func (r *redfish) getSystem(ctx context.Context) (string, *redfishSystem, error) {
	path, err := r.systemPath(ctx)
	if err != nil {
		return "", nil, err
	}
	var sys redfishSystem
	if err := r.do(ctx, http.MethodGet, path, nil, &sys); err != nil {
		return "", nil, err
	}
	return path, &sys, nil
}

func (r *redfish) Probe(ctx context.Context) (Info, error) {
	var info Info
	var root struct {
		Version string `json:"RedfishVersion"`
	}
	if err := r.do(ctx, http.MethodGet, redfishRoot, nil, &root); err != nil {
		return info, err
	}
	info.Version = "Redfish " + root.Version
	_, sys, err := r.getSystem(ctx)
	if err != nil {
		return info, err
	}
	info.Manufacturer, info.Model, info.Serial, info.UUID = sys.Manufacturer, sys.Model, sys.SerialNumber, strings.ToLower(sys.UUID)
	info.Power = redfishPower(sys.PowerState)
	info.CPUs = sys.Processors.Count
	info.MemoryBytes = int64(sys.Memory.GiB * float64(1<<30))
	info.MAC = r.firstMAC(ctx, sys.Ethernet.ID)
	info.Disks = r.drives(ctx, sys.Storage.ID)
	return info, nil
}

func redfishPower(state string) string {
	switch state {
	case "On", "PoweringOn":
		return "on"
	case "Off", "PoweringOff":
		return "off"
	case "":
		return "unknown"
	}
	return strings.ToLower(state)
}

func (r *redfish) firstMAC(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	var col struct {
		Members []redfishRef `json:"Members"`
	}
	if err := r.do(ctx, http.MethodGet, path, nil, &col); err != nil {
		r.tracef("ethernet interfaces: %v", err)
		return ""
	}
	for _, m := range col.Members {
		var nic struct {
			MAC       string `json:"MACAddress"`
			Permanent string `json:"PermanentMACAddress"`
		}
		if err := r.do(ctx, http.MethodGet, m.ID, nil, &nic); err != nil {
			continue
		}
		mac := nic.Permanent
		if mac == "" {
			mac = nic.MAC
		}
		if mac != "" {
			return strings.ToLower(strings.ReplaceAll(mac, "-", ":"))
		}
	}
	return ""
}

func (r *redfish) drives(ctx context.Context, path string) []DiskInfo {
	if path == "" {
		return nil
	}
	var col struct {
		Members []redfishRef `json:"Members"`
	}
	if err := r.do(ctx, http.MethodGet, path, nil, &col); err != nil {
		r.tracef("storage: %v", err)
		return nil
	}
	var out []DiskInfo
	for _, m := range col.Members {
		var ctl struct {
			Drives []redfishRef `json:"Drives"`
		}
		if err := r.do(ctx, http.MethodGet, m.ID, nil, &ctl); err != nil {
			continue
		}
		for _, d := range ctl.Drives {
			var drv struct {
				Model     string `json:"Model"`
				Serial    string `json:"SerialNumber"`
				Capacity  int64  `json:"CapacityBytes"`
				MediaType string `json:"MediaType"`
				Protocol  string `json:"Protocol"`
			}
			if err := r.do(ctx, http.MethodGet, d.ID, nil, &drv); err != nil {
				continue
			}
			out = append(out, DiskInfo{Model: drv.Model, Serial: drv.Serial, SizeBytes: drv.Capacity, Transport: strings.ToLower(drv.Protocol), Media: strings.ToLower(drv.MediaType)})
		}
	}
	return out
}

func (r *redfish) Power(ctx context.Context, act Action) error {
	path, sys, err := r.getSystem(ctx)
	if err != nil {
		return err
	}
	var reset string
	switch act {
	case PowerOn:
		reset = "On"
	case PowerOff:
		reset = "ForceOff"
	case Reset:
		reset = "ForceRestart"
	case PowerCycle:
		reset = "PowerCycle"
	case BootPXE:
		if err := r.forcePXE(ctx, path, sys); err != nil {
			return err
		}
		if redfishPower(sys.PowerState) == "on" {
			reset = "ForceRestart"
		} else {
			reset = "On"
		}
	default:
		return fmt.Errorf("unknown power action %q", act)
	}
	reset = pickReset(reset, sys.Actions.Reset.Allowed)
	target := sys.Actions.Reset.Target
	if target == "" {
		target = path + "/Actions/ComputerSystem.Reset"
	}
	if err := r.do(ctx, http.MethodPost, target, map[string]string{"ResetType": reset}, nil); err != nil {
		return err
	}
	r.tracef("ComputerSystem.Reset %s accepted", reset)
	return nil
}

func contains(list []string, s string) bool {
	for _, a := range list {
		if a == s {
			return true
		}
	}
	return false
}

func pickReset(want string, allowed []string) string {
	if len(allowed) == 0 {
		return want
	}
	has := func(s string) bool { return contains(allowed, s) }
	if has(want) {
		return want
	}
	switch want {
	case "ForceRestart":
		for _, alt := range []string{"GracefulRestart", "PowerCycle"} {
			if has(alt) {
				return alt
			}
		}
	case "ForceOff":
		if has("GracefulShutdown") {
			return "GracefulShutdown"
		}
	case "PowerCycle":
		if has("ForceRestart") {
			return "ForceRestart"
		}
	}
	return want
}

func (r *redfish) forcePXE(ctx context.Context, path string, sys *redfishSystem) error {
	if len(sys.Boot.Allowed) > 0 && !contains(sys.Boot.Allowed, "Pxe") {
		return fmt.Errorf("BMC at %s: this system does not offer a PXE boot override (allowed: %s)", r.c.Host, strings.Join(sys.Boot.Allowed, ", "))
	}
	body := map[string]any{"Boot": map[string]string{"BootSourceOverrideEnabled": "Once", "BootSourceOverrideTarget": "Pxe"}}
	if err := r.do(ctx, http.MethodPatch, path, body, nil); err != nil {
		return fmt.Errorf("boot override: %w", err)
	}
	r.tracef("boot override Once/Pxe accepted")
	return nil
}

func ProbeRedfish(ctx context.Context, host string, timeout time.Duration) (string, bool) {
	r := newRedfish(Config{Host: host}, nil)
	r.client.Timeout = timeout
	var root struct {
		Version string `json:"RedfishVersion"`
	}
	if err := r.do(ctx, http.MethodGet, redfishRoot, nil, &root); err != nil || root.Version == "" {
		return "", false
	}
	return root.Version, true
}
