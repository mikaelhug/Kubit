package pxe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Boot is what the server has seen from one machine, keyed by MAC (DHCP) and later
// matched to the IP that fetched the boot script and assets.
type Boot struct {
	MAC       string    `json:"mac"`
	IP        string    `json:"ip,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	// Stage: nopxe (asked for an address without PXE), dhcp (firmware asked),
	// ipxe (iPXE fetched the script), kernel (assets served)
	Stage string `json:"stage"`
	Class string `json:"class,omitempty"`
	Count int    `json:"count"`
}

// Status is exported on /status.json for the daemon's PXE page.
type Status struct {
	StartedAt    time.Time `json:"startedAt"`
	Interface    string    `json:"interface"`
	HTTPOnly     bool      `json:"httpOnly"`
	IP           string    `json:"ip"`
	HTTPPort     int       `json:"httpPort"`
	TalosVersion string    `json:"talosVersion"`
	SchematicID  string    `json:"schematicId"`
	Boots        []Boot    `json:"boots"`
	Log          []string  `json:"log"`
}

type tracker struct {
	mu      sync.Mutex
	started time.Time
	boots   map[string]*Boot // by MAC
	byIP    map[string]string
	log     []string
}

func newTracker() *tracker {
	return &tracker{started: time.Now(), boots: map[string]*Boot{}, byIP: map[string]string{}}
}

func (t *tracker) dhcp(mac, arch string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.boots[mac]
	if b == nil {
		b = &Boot{MAC: mac, FirstSeen: time.Now()}
		t.boots[mac] = b
	}
	b.LastSeen = time.Now()
	b.Count++
	if b.Stage == "" || b.Stage == "nopxe" {
		b.Stage = "dhcp"
	}
	if arch != "" {
		b.Arch = arch
	}
}

// plain records a DHCP discover without PXE options: the machine is up but not
// network-booting. Logged once a minute per MAC so a chatty client does not flood.
func (t *tracker) plain(mac, class string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.boots[mac]
	if b == nil {
		b = &Boot{MAC: mac, FirstSeen: time.Now(), Stage: "nopxe"}
		t.boots[mac] = b
	}
	if b.Stage != "nopxe" {
		return
	}
	recent := time.Since(b.LastSeen) < time.Minute
	b.LastSeen = time.Now()
	b.Count++
	b.Class = class
	if recent {
		return
	}
	what := "no vendor class"
	if class != "" {
		what = "vendor class " + class
	}
	t.log = append(t.log, time.Now().Format("15:04:05")+fmt.Sprintf(" %s asked for an address without PXE (%s): it is booting from disk or its management engine woke", mac, what))
}

// http records a script or asset fetch. The IP is all HTTP knows; it is attributed to
// the most recent DHCP client still without an address.
func (t *tracker) http(ip, arch, stage string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mac := t.byIP[ip]
	if mac == "" {
		var latest *Boot
		for _, b := range t.boots {
			if b.IP == "" && (latest == nil || b.LastSeen.After(latest.LastSeen)) {
				latest = b
			}
		}
		if latest != nil {
			latest.IP = ip
			t.byIP[ip] = latest.MAC
			mac = latest.MAC
		}
	}
	b := t.boots[mac]
	if b == nil {
		b = &Boot{MAC: "unknown@" + ip, IP: ip, FirstSeen: time.Now()}
		t.boots[b.MAC] = b
		t.byIP[ip] = b.MAC
	}
	b.LastSeen = time.Now()
	if arch != "" {
		b.Arch = arch
	}
	if stage == "kernel" || b.Stage != "kernel" {
		b.Stage = stage
	}
}

func (t *tracker) logf(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = append(t.log, time.Now().Format("15:04:05")+" "+line)
	if len(t.log) > 500 {
		t.log = t.log[len(t.log)-400:]
	}
}

func (t *tracker) status(s *Server) Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := Status{StartedAt: t.started, Interface: s.Interface, HTTPOnly: s.HTTPOnly, IP: s.IP.String(), HTTPPort: s.HTTPPort, TalosVersion: s.Profile.TalosVersion, SchematicID: s.Profile.SchematicID, Log: append([]string{}, t.log...), Boots: []Boot{}}
	for _, b := range t.boots {
		st.Boots = append(st.Boots, *b)
	}
	sort.Slice(st.Boots, func(i, j int) bool { return st.Boots[i].LastSeen.After(st.Boots[j].LastSeen) })
	return st
}

func (s *Server) statusHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.track.status(s))
}
