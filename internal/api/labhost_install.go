package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/oob"
	"github.com/mikael/kubit/internal/oob/ider"
	"github.com/mikael/kubit/internal/store"
)

// handleMachineAdd registers a machine Kubit has not seen on the network: a VM or a
// box that will be installed by hand. Nothing is probed; discovery fills in the rest.
func (s *Server) handleMachineAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MAC, IP, Hostname, Arch string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	mac := strings.ToLower(strings.TrimSpace(req.MAC))
	if _, err := net.ParseMAC(mac); err != nil {
		http.Error(w, "mac: a MAC address is required", http.StatusBadRequest)
		return
	}
	row := store.NodeRow{MAC: mac, IP: req.IP, Hostname: req.Hostname, Arch: req.Arch, Source: "manual", State: "unknown"}
	if existing, err := s.store.GetMachine(r.Context(), mac); err == nil {
		row.State = existing.State
	}
	if err := s.store.UpsertNode(r.Context(), row); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "machine.add", mac)
	m, _ := s.store.GetMachine(r.Context(), mac)
	writeJSON(w, http.StatusCreated, m)
}

// mediaBoot is one machine booting from a virtual CD: the IDE-R session feeding
// the image, and what the firmware has done with it so far.
type mediaBoot struct {
	events    chan ider.Event
	done      chan error
	cancel    context.CancelFunc
	sink      clusterSink
	mac, step string

	opened    bool
	firstRead bool
	bytes     int64
	lastRead  time.Time
	lastLog   time.Time
	closed    string
}

// startMediaBoot opens the session (AMT accepts it while the host is off) and
// arms one CD boot; the session runs until stop or AMT closes it.
func (s *Server) startMediaBoot(ctx context.Context, m *store.Machine, cfg *oob.Config, iso string, sink clusterSink) (*mediaBoot, error) {
	media, err := ider.OpenMedia(iso)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	b := &mediaBoot{events: make(chan ider.Event, 256), done: make(chan error, 1), cancel: cancel, sink: sink, mac: m.MAC}
	go func() {
		defer media.Close()
		b.done <- ider.Serve(sctx, ider.Config{Host: cfg.Host, User: cfg.User, Password: cfg.Password, TLS: cfg.TLS, Trace: os.Getenv("KUBIT_IDER_TRACE") != ""}, media, b.events)
	}()
	return b, nil
}

func (b *mediaBoot) stop() {
	b.cancel()
	select {
	case <-b.done:
	case <-time.After(5 * time.Second):
	}
}

// poll drains events into the log and the phase state.
func (b *mediaBoot) poll() {
	for {
		select {
		case e := <-b.events:
			switch e.Kind {
			case ider.Authenticated:
				b.log(cluster.Info, "AMT accepted the redirection session")
			case ider.Opened:
				b.opened = true
				b.log(cluster.Info, fmt.Sprintf("virtual CD attached (%d-byte reads)", e.Buffer))
			case ider.FirstRead:
				b.firstRead = true
				b.lastRead = time.Now()
				b.log(cluster.Info, "firmware is reading the CD")
			case ider.Progress:
				b.bytes = e.Bytes
				b.lastRead = time.Now()
				if time.Since(b.lastLog) > 5*time.Second {
					b.lastLog = time.Now()
					b.log(cluster.Info, fmt.Sprintf("read %.1f MB from the CD", float64(e.Bytes)/1e6))
				}
			case ider.Reset:
				b.log(cluster.Info, "machine reset")
			case ider.Closed:
				b.closed = e.Reason
				b.log(cluster.Info, "session closed: "+e.Reason)
			}
		default:
			return
		}
	}
}

func (b *mediaBoot) log(level cluster.Level, msg string) {
	b.sink(clusterEvent{Time: time.Now(), Kind: "log", Level: level, Step: b.step, Node: b.mac, Message: msg})
}

var (
	labMediaWait     = 1 * time.Minute
	labBootWait      = 3 * time.Minute
	labLoadStall     = 2 * time.Minute
	labLoadWait      = 10 * time.Minute
	labInstallerWait = 5 * time.Minute
	labInstallWait   = 30 * time.Minute
	labSSHWait       = 5 * time.Minute
	labPollEvery     = 5 * time.Second
)

// phase polls check until it reports done or the budget is spent; the last note
// becomes the error.
func phase(ctx context.Context, sink clusterSink, b *mediaBoot, mac, step string, budget time.Duration, check func() (bool, string)) error {
	if b != nil {
		b.step = step
	}
	sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepRunning})
	deadline := time.Now().Add(budget)
	for {
		if b != nil {
			b.poll()
		}
		done, note := check()
		if done {
			if note != "" {
				sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Done, Step: step, Node: mac, Message: note})
			}
			sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepDone})
			return nil
		}
		if b != nil && b.closed != "" {
			return fmt.Errorf("the redirection session ended (%s) before the machine finished booting from it", b.closed)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s", note)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(labPollEvery):
		}
	}
}

// bootFromMedia runs media → power → boot: the session is open, the boot override
// set and the machine reset, and the firmware has started reading the CD.
func (s *Server) bootFromMedia(ctx context.Context, m *store.Machine, cfg *oob.Config, iso string, sink clusterSink) (*mediaBoot, error) {
	mgr, err := oob.Open(*cfg)
	if err != nil {
		return nil, err
	}
	if err := mgr.PrepareRedirection(ctx); err != nil {
		return nil, err
	}
	b, err := s.startMediaBoot(ctx, m, cfg, iso, sink)
	if err != nil {
		return nil, err
	}
	if err := phase(ctx, sink, b, m.MAC, "media", labMediaWait, func() (bool, string) {
		select {
		case err := <-b.done:
			b.done <- err
			return false, "AMT refused the redirection session: " + errString(err)
		default:
		}
		if b.opened {
			return true, ""
		}
		return false, "AMT did not accept the redirection session within " + labMediaWait.String() + ": IDE-R is disabled or another console holds a session"
	}); err != nil {
		b.stop()
		return nil, err
	}
	if err := phase(ctx, sink, b, m.MAC, "power", 30*time.Second, func() (bool, string) {
		if err := mgr.Power(ctx, oob.BootMedia); err != nil {
			return false, err.Error()
		}
		return true, "boot override set to the virtual CD; machine reset"
	}); err != nil {
		b.stop()
		return nil, err
	}
	if err := phase(ctx, sink, b, m.MAC, "boot", labBootWait, func() (bool, string) {
		if b.firstRead {
			return true, "firmware booted from the virtual CD"
		}
		return false, "the machine never read the virtual CD within " + labBootWait.String() + ": it did not restart, or its firmware ignored the CD boot override. Check that it powered on and that no other boot source comes first."
	}); err != nil {
		b.stop()
		return nil, err
	}
	return b, nil
}

func errString(err error) string {
	if err == nil {
		return "closed"
	}
	return err.Error()
}

// labWaitInstall follows one Debian install from the boot media to SSH, one phase
// at a time, each with its own timeout and a diagnosis that names the layer that
// failed. b is nil for a manual boot (the operator booted the installer).
func (s *Server) labWaitInstall(ctx context.Context, m *store.Machine, b *mediaBoot, sink clusterSink) (*labhost.Client, error) {
	mac := m.MAC
	stageAt := func() (string, time.Time) {
		row, err := s.store.GetMachine(ctx, mac)
		if err != nil || row.LabHost == nil || row.LabHost.Install == nil {
			return "", time.Time{}
		}
		t, _ := time.Parse(time.RFC3339, row.LabHost.Install.At)
		return row.LabHost.Install.Stage, t
	}
	stageRank := map[string]int{"installer": 1, "partitioning": 2, "packages": 3, "late-done": 4, "booted": 5}
	wait := func(step string, budget time.Duration, check func() (bool, string)) error {
		return phase(ctx, sink, b, mac, step, budget, check)
	}
	if b != nil {
		if err := wait("load", labLoadWait, func() (bool, string) {
			if st, _ := stageAt(); stageRank[st] >= 1 {
				return true, fmt.Sprintf("installer loaded (%.1f MB read)", float64(b.bytes)/1e6)
			}
			if time.Since(b.lastRead) > labLoadStall {
				return false, fmt.Sprintf("the firmware read %.1f MB then stopped without the installer reporting in: the image did not boot. Secure Boot must be off (the installer's loader is unsigned); otherwise the installer found no network or cannot reach the preseed feed.", float64(b.bytes)/1e6)
			}
			return false, fmt.Sprintf("still reading the CD (%.1f MB) after %s", float64(b.bytes)/1e6, labLoadWait)
		}); err != nil {
			return nil, err
		}
	} else if err := wait("installer", labInstallerWait, func() (bool, string) {
		st, at := stageAt()
		if stageRank[st] >= 1 {
			return true, "installer running (" + st + " at " + at.Local().Format("15:04:05") + ")"
		}
		return false, "the kernel booted but the installer never reported in: it found no network, or the preseed feed is unreachable from the machine"
	}); err != nil {
		return nil, err
	}
	if err := wait("install", labInstallWait, func() (bool, string) {
		st, at := stageAt()
		if stageRank[st] >= 4 {
			return true, "Debian installed; rebooting"
		}
		return false, fmt.Sprintf("the installer stopped after %q (%s ago): it is waiting on a question, usually partitioning or a mirror error. Attach a screen to see it.", st, time.Since(at).Round(time.Minute))
	}); err != nil {
		return nil, err
	}
	priv, _, err := s.store.SSHKey(ctx)
	if err != nil {
		return nil, err
	}
	var lc *labhost.Client
	if err := wait("ssh", labSSHWait, func() (bool, string) {
		st, _ := stageAt()
		// The row's address may have been updated meanwhile (discovery, or a harness
		// that learned the lease), so read it again each time.
		rowIP := ""
		if row, err := s.store.GetMachine(ctx, mac); err == nil {
			rowIP = row.IP
		}
		for _, ip := range uniq(m.IP, rowIP) {
			if ip == "" {
				continue
			}
			d := net.Dialer{Timeout: 2 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "22"))
			if err != nil {
				continue
			}
			conn.Close()
			cl, err := labhost.Dial(ctx, ip, priv)
			if err != nil {
				continue
			}
			if _, err := cl.Run(ctx, "test -f /var/lib/kubit/READY"); err == nil {
				lc = cl
				m.IP = ip
				return true, "SSH answers at " + ip + " as " + labhost.User
			}
			cl.Close()
		}
		if st == "booted" {
			return false, "Debian booted and reported in, but SSH with Kubit's key is refused: the key was not installed (late_command failed) or sshd is not running"
		}
		return false, "Debian installed but the machine did not come back on SSH: it may have booted its old OS — check the boot order puts the new disk first"
	}); err != nil {
		return nil, err
	}
	return lc, nil
}
