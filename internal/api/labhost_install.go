package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/pxe"
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

// handleLabProgress is what the installer (through the pxe proxy) calls at each stage.
func (s *Server) handleLabProgress(w http.ResponseWriter, r *http.Request) {
	mac, stage := strings.ToLower(r.URL.Query().Get("mac")), r.URL.Query().Get("stage")
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil || m.LabHost == nil {
		http.Error(w, "unknown lab host", http.StatusNotFound)
		return
	}
	m.LabHost.Install = &store.InstallProgress{Stage: stage, At: time.Now().UTC().Format(time.RFC3339)}
	if err := s.store.SetLabHost(r.Context(), mac, m.LabHost); err != nil {
		writeErr(w, err)
		return
	}
	// The installer's address is the lease the installed system will most likely
	// keep; it is where the SSH phase looks first.
	if ip := r.URL.Query().Get("ip"); ip != "" && ip != m.IP && net.ParseIP(ip) != nil {
		_ = s.store.UpsertNode(r.Context(), store.NodeRow{MAC: mac, IP: ip, Source: m.Source, State: m.State, Hostname: m.Hostname, Arch: m.Arch})
	}
	w.WriteHeader(http.StatusNoContent)
}

// pxeStatus reads the PXE server's view (boots by MAC and its log).
func (s *Server) pxeStatus(ctx context.Context) (*pxe.Status, error) {
	v, err := s.store.GetSettings(ctx)
	if err != nil || v.PXEStatusURL == "" {
		return nil, fmt.Errorf("no PXE status URL")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(v.PXEStatusURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var st pxe.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

// pxeWatch mirrors new PXE log lines about a machine into an operation as they
// appear, so the Activity drawer reads like the console nobody is sitting at.
type pxeWatch struct {
	s    *Server
	mac  string
	ip   string
	seen int
	sink clusterSink
	step string
}

func (p *pxeWatch) poll(ctx context.Context) *pxe.Boot {
	st, err := p.s.pxeStatus(ctx)
	if err != nil {
		return nil
	}
	if len(st.Log) < p.seen {
		p.seen = 0
	}
	for _, line := range st.Log[p.seen:] {
		if strings.Contains(line, p.mac) || (p.ip != "" && strings.Contains(line, p.ip+" ")) {
			p.sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Info, Step: p.step, Node: p.mac, Message: "pxe: " + strings.TrimSpace(line)})
		}
	}
	p.seen = len(st.Log)
	for i := range st.Boots {
		if strings.EqualFold(st.Boots[i].MAC, p.mac) {
			if st.Boots[i].IP != "" {
				p.ip = st.Boots[i].IP
			}
			return &st.Boots[i]
		}
	}
	return nil
}

// Phase timeouts: each is the longest a healthy install spends there, with margin.
// Variables so tests can shorten them.
var (
	labBootWait      = 3 * time.Minute
	labIPXEWait      = 2 * time.Minute
	labInstallerWait = 5 * time.Minute
	labInstallWait   = 30 * time.Minute
	labSSHWait       = 5 * time.Minute
	labPollEvery     = 5 * time.Second
)

// labWaitBoot is the PXE half shared by lab-host installs and Boot into Talos: the
// firmware's DHCP request, then the kernel fetch.
func (s *Server) labWaitBoot(ctx context.Context, watch *pxeWatch) error {
	sink, mac := watch.sink, watch.mac
	wait := func(step string, budget time.Duration, check func() (bool, string)) error {
		watch.step = step
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepRunning})
		deadline := time.Now().Add(budget)
		for {
			done, note := check()
			if done {
				sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Done, Step: step, Node: mac, Message: note})
				sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepDone})
				return nil
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
	if err := wait("boot", labBootWait, func() (bool, string) {
		if b := watch.poll(ctx); b != nil && b.Stage != "nopxe" {
			return true, fmt.Sprintf("network boot request from %s (%s firmware)", mac, b.Arch)
		} else if b != nil {
			return false, fmt.Sprintf("%s asked for an address without PXE (vendor class %q): the machine came up from its disk, or only its management engine did. Check the BIOS boot order (network boot first, UEFI IPv4 PXE enabled) and that the AMT boot override is honoured.", mac, b.Class)
		}
		return false, fmt.Sprintf("no network boot request from %s within %s. The machine booted from its disk or another PXE server answered first: check the BIOS boot order and that the AMT boot override is honoured. Kubit's PXE server answers on the interface it was started with — on a laptop that is usually Wi-Fi, and some access points drop DHCP replies; a wired interface is safer.", mac, labBootWait)
	}); err != nil {
		return err
	}
	return wait("ipxe", labIPXEWait, func() (bool, string) {
		if b := watch.poll(ctx); b != nil && b.Stage == "kernel" {
			return true, "kernel and initrd fetched"
		}
		return false, "PXE answered but the kernel was never fetched: TFTP (69) or HTTP (8069) is blocked between the machine and this host, or iPXE failed to get an address from the LAN's DHCP"
	})
}

// labWaitInstall follows one Debian install from the network boot to SSH, one phase
// at a time, each with its own timeout and a diagnosis that names the layer that
// failed instead of "no SSH after 30 minutes". manual skips the PXE phases (the
// operator booted the installer themselves).
func (s *Server) labWaitInstall(ctx context.Context, m *store.Machine, manual bool, sink clusterSink) (*labhost.Client, error) {
	mac := m.MAC
	watch := &pxeWatch{s: s, mac: mac, sink: sink}
	stageAt := func() (string, time.Time) {
		row, err := s.store.GetMachine(ctx, mac)
		if err != nil || row.LabHost == nil || row.LabHost.Install == nil {
			return "", time.Time{}
		}
		t, _ := time.Parse(time.RFC3339, row.LabHost.Install.At)
		return row.LabHost.Install.Stage, t
	}
	stageRank := map[string]int{"installer": 1, "partitioning": 2, "packages": 3, "late-done": 4, "booted": 5}
	// wait polls check every 5 s until it reports done or the phase's budget is spent.
	wait := func(step string, budget time.Duration, check func() (bool, string)) error {
		watch.step = step
		sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepRunning})
		deadline := time.Now().Add(budget)
		for {
			watch.poll(ctx)
			done, note := check()
			if done {
				if note != "" {
					sink(clusterEvent{Time: time.Now(), Kind: "log", Level: cluster.Done, Step: step, Node: mac, Message: note})
				}
				sink(clusterEvent{Time: time.Now(), Kind: "step", Step: step, Status: cluster.StepDone})
				return nil
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

	if !manual {
		if err := s.labWaitBoot(ctx, watch); err != nil {
			return nil, err
		}
	}
	if err := wait("installer", labInstallerWait, func() (bool, string) {
		st, at := stageAt()
		if stageRank[st] >= 1 {
			return true, "installer running (" + st + " at " + at.Local().Format("15:04:05") + ")"
		}
		return false, "the kernel booted but the installer never reported in: it found no network (wrong initrd for this hardware, or DHCP failed inside the installer) or the preseed URL is unreachable from the machine"
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
		for _, ip := range uniq(m.IP, rowIP, watch.ip) {
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
