package pxe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

type Boot struct {
	MAC       string    `json:"mac"`
	IP        string    `json:"ip,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Stage     string    `json:"stage"`
	Class     string    `json:"class,omitempty"`
	Count     int       `json:"count"`
}

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
	boots   map[string]*Boot
	byIP    map[string]string
	log     []string
	wake    chan struct{}
}

func newTracker() *tracker {
	return &tracker{started: time.Now(), boots: map[string]*Boot{}, byIP: map[string]string{}, wake: make(chan struct{})}
}

func (t *tracker) notify() {
	close(t.wake)
	t.wake = make(chan struct{})
}

func (t *tracker) changes() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wake
}

func (t *tracker) dhcp(mac, arch string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.notify()
	b := t.boots[mac]
	if b == nil {
		b = &Boot{MAC: mac, FirstSeen: time.Now()}
		t.add(b)
	} else if time.Since(b.LastSeen) > time.Minute {
		delete(t.byIP, b.IP)
		*b = Boot{MAC: mac, FirstSeen: time.Now()}
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

func (t *tracker) plain(mac, class string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.notify()
	b := t.boots[mac]
	if b == nil {
		b = &Boot{MAC: mac, FirstSeen: time.Now(), Stage: "nopxe"}
		t.add(b)
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
	t.appendLog(fmt.Sprintf("%s asked for an address without PXE (%s): it is booting from disk or its management engine woke", mac, what))
}

func (t *tracker) http(ip, arch, stage string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.notify()
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
		t.add(b)
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
	defer t.notify()
	t.appendLog(line)
}

func (t *tracker) appendLog(line string) {
	t.log = append(t.log, time.Now().Format("15:04:05")+" "+line)
	if len(t.log) > 500 {
		t.log = t.log[len(t.log)-400:]
	}
}

const (
	maxBoots = 512
	bootTTL  = 24 * time.Hour
)

func (t *tracker) add(b *Boot) {
	if len(t.boots) >= maxBoots {
		t.prune(time.Now())
	}
	t.boots[b.MAC] = b
}

func (t *tracker) prune(now time.Time) {
	for mac, b := range t.boots {
		if now.Sub(b.LastSeen) > bootTTL {
			t.drop(mac, b)
		}
	}
	if len(t.boots) < maxBoots {
		return
	}
	all := make([]*Boot, 0, len(t.boots))
	for _, b := range t.boots {
		all = append(all, b)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].LastSeen.Before(all[j].LastSeen) })
	for _, b := range all[:len(all)-maxBoots+1] {
		t.drop(b.MAC, b)
	}
}

func (t *tracker) drop(mac string, b *Boot) {
	delete(t.boots, mac)
	if b.IP != "" && t.byIP[b.IP] == mac {
		delete(t.byIP, b.IP)
	}
}

func (t *tracker) status(s *Server) Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := Status{StartedAt: t.started, Interface: s.Interface, HTTPOnly: s.HTTPOnly, IP: s.address(), HTTPPort: s.HTTPPort, TalosVersion: s.Profile.TalosVersion, SchematicID: s.Profile.SchematicID, Log: append([]string{}, t.log...), Boots: []Boot{}}
	for _, b := range t.boots {
		st.Boots = append(st.Boots, *b)
	}
	sort.Slice(st.Boots, func(i, j int) bool { return st.Boots[i].LastSeen.After(st.Boots[j].LastSeen) })
	return st
}

const streamBatch = 250 * time.Millisecond

func (s *Server) statusHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !r.URL.Query().Has("watch") || !ok {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.track.status(s))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	for {
		changed := s.track.changes()
		b, err := json.Marshal(s.track.status(s))
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(streamBatch):
		}
	}
}
