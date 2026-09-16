package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
)

// Feed is what a Debian installer fetches from Kubit while it runs: its preseed,
// the post-install script, and where to report progress. It is unauthenticated
// (the installer has no token) and answers only for hosts that are installing.
type Feed struct {
	Store *store.Store
	Port  int
}

func (f *Feed) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /labhost/{mac}/preseed", f.preseed)
	mux.HandleFunc("GET /labhost/{mac}/postinstall", f.postinstall)
	mux.HandleFunc("GET /labhost/{mac}/progress", f.progress)
	return mux
}

// Base is the URL the installer on a machine at target can reach this feed at:
// the daemon host's address on the route to it.
func (f *Feed) Base(target string) (string, error) {
	c, err := net.Dial("udp", net.JoinHostPort(target, "16992"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	return fmt.Sprintf("http://%s:%d", c.LocalAddr().(*net.UDPAddr).IP, f.Port), nil
}

func (f *Feed) installing(r *http.Request) (*store.Machine, bool) {
	mac := strings.ToLower(r.PathValue("mac"))
	m, err := f.Store.GetMachine(r.Context(), mac)
	if err != nil || m.LabHost == nil || m.LabHost.State != "installing" {
		return nil, false
	}
	return m, true
}

func (f *Feed) preseed(w http.ResponseWriter, r *http.Request) {
	m, ok := f.installing(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, pub, err := f.Store.SSHKey(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	disk := ""
	if len(m.Hardware) > 2 {
		var inv talos.Inventory
		if json.Unmarshal(m.Hardware, &inv) == nil {
			if cands := inv.InstallCandidates(); len(cands) > 0 {
				sort.Slice(cands, func(i, j int) bool { return cands[i].SizeBytes > cands[j].SizeBytes })
				disk = cands[0].DevPath
			}
		}
	}
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = m.Arch
	}
	base := fmt.Sprintf("http://%s/labhost/%s", r.Host, m.MAC)
	out, err := labhost.Preseed(labhost.PreseedParams{Hostname: Hostname(m), Disk: disk, PublicKey: pub, PostURL: base + "/postinstall", Timezone: r.URL.Query().Get("tz"), Arch: arch})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(out))
}

func (f *Feed) postinstall(w http.ResponseWriter, r *http.Request) {
	m, ok := f.installing(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(labhost.PostInstall(fmt.Sprintf("http://%s/labhost/%s/progress", r.Host, m.MAC))))
}

func (f *Feed) progress(w http.ResponseWriter, r *http.Request) {
	m, ok := f.installing(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	m.LabHost.Install = &store.InstallProgress{Stage: r.URL.Query().Get("stage"), At: time.Now().UTC().Format(time.RFC3339)}
	if err := f.Store.SetLabHost(r.Context(), m.MAC, m.LabHost); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && ip != m.IP && net.ParseIP(ip) != nil {
		_ = f.Store.UpsertNode(r.Context(), store.NodeRow{MAC: m.MAC, IP: ip, Source: m.Source, State: m.State, Hostname: m.Hostname, Arch: m.Arch})
	}
	w.WriteHeader(http.StatusNoContent)
}

// Hostname names a lab host after its serial, else the tail of its MAC.
func Hostname(m *store.Machine) string {
	if m.Serial != "" {
		return "lab-" + strings.ToLower(strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, m.Serial))
	}
	return "lab-" + strings.ReplaceAll(m.MAC[9:], ":", "")
}

// Serve runs the feed until ctx ends.
func (f *Feed) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: f.Handler()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
