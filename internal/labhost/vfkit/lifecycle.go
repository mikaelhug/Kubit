package vfkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mikael/kubit/internal/labhost"
)

func (h *Host) Define(ctx context.Context, s labhost.VMSpec) error {
	if err := checkName(s.Name); err != nil {
		return err
	}
	if strings.Contains(h.Dir, ",") {
		return errors.New("Kubit's home path contains a comma, which vfkit cannot take")
	}
	if _, err := os.Stat(h.file(s.Name, "spec.json")); err == nil {
		return fmt.Errorf("%s already exists", s.Name)
	}
	if len(h.sock(s.Name)) > maxSock {
		return fmt.Errorf("%s: the VM's control socket path is longer than macOS allows; use a shorter name or Kubit home", s.Name)
	}
	if err := os.MkdirAll(h.vmDir(s.Name), 0o700); err != nil {
		return err
	}
	if err := sparse(h.file(s.Name, "disk.raw"), s.DiskGiB, false); err != nil {
		return err
	}
	if s.DataGiB > 0 {
		if err := sparse(h.file(s.Name, "data.raw"), s.DataGiB, false); err != nil {
			return err
		}
	}
	sp := &spec{Name: s.Name, MAC: strings.ToLower(s.MAC), CPUs: s.CPUs, MemMiB: s.MemMiB, DiskGiB: s.DiskGiB, DataGiB: s.DataGiB, Boot: "disk", ISO: s.ISO}
	if s.ISO != "" {
		sp.Boot = "talos"
	}
	if err := h.writeSpec(sp); err != nil {
		return err
	}
	if err := h.Start(ctx, s.Name); err != nil {
		_ = h.Delete(context.WithoutCancel(ctx), s.Name)
		return err
	}
	return nil
}

func sparse(path string, gib int, replace bool) error {
	if !replace {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(int64(gib) << 30)
}

func (h *Host) Start(ctx context.Context, name string) error {
	launched, err := h.launch(ctx, name)
	if err != nil || !launched {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if st, err := h.state(ctx, name); err == nil && strings.HasSuffix(st, "Running") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not start: %s", name, tail(h.file(name, "vfkit.log"), 5))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (h *Host) launch(ctx context.Context, name string) (bool, error) {
	specMu.Lock()
	defer specMu.Unlock()
	sp, err := h.readSpec(name)
	if err != nil {
		return false, err
	}
	jobs, err := h.jobs(ctx)
	if err != nil {
		return false, err
	}
	if j, ok := jobs[h.label(name)]; ok && j.PID > 0 {
		if !sp.Run {
			sp.Run = true
			return false, h.writeSpec(sp)
		}
		return false, nil
	}
	if err := os.Chmod(h.vmDir(name), 0o700); err != nil {
		return false, err
	}
	if sp.Wipe {
		if err := sparse(h.file(name, "disk.raw"), sp.DiskGiB, true); err != nil {
			return false, err
		}
		if err := os.Remove(h.file(name, "efi-vars")); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		sp.Wipe = false
	}
	if sp.Boot == "talos" {
		if _, err := os.Stat(sp.ISO); err != nil {
			return false, fmt.Errorf("%s: the Talos ISO is missing (%s); set the lab host up again", name, sp.ISO)
		}
	}
	sp.Run = true
	if err := h.writeSpec(sp); err != nil {
		return false, err
	}
	path, err := h.writePlist(sp)
	if err != nil {
		return false, err
	}
	_, _ = h.Run(ctx, "launchctl", "bootout", h.Domain+"/"+h.label(name))
	if err := os.Remove(h.sock(name)); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if _, err := h.Run(ctx, "launchctl", "bootstrap", h.Domain, path); err != nil {
		return false, err
	}
	return true, nil
}

var specMu sync.Mutex

func (h *Host) update(name string, fn func(*spec) error) (*spec, error) {
	specMu.Lock()
	defer specMu.Unlock()
	sp, err := h.readSpec(name)
	if err != nil {
		return nil, err
	}
	if err := fn(sp); err != nil {
		return nil, err
	}
	if err := h.writeSpec(sp); err != nil {
		return nil, err
	}
	if _, err := h.writePlist(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

func (h *Host) Stop(ctx context.Context, name string, force bool) error {
	if _, err := h.update(name, func(sp *spec) error { sp.Run = false; return nil }); err != nil {
		return err
	}
	if !force {
		if err := h.setState(ctx, name, "Stop"); err != nil && !refused(err) {
			return err
		}
		return nil
	}
	_ = h.setState(ctx, name, "HardStop")
	return h.unload(ctx, name)
}

func (h *Host) unload(ctx context.Context, name string) error {
	_, _ = h.Run(ctx, "launchctl", "bootout", h.Domain+"/"+h.label(name))
	deadline := time.Now().Add(15 * time.Second)
	for {
		jobs, err := h.jobs(ctx)
		if err != nil {
			return err
		}
		if _, ok := jobs[h.label(name)]; !ok && !h.answers(ctx, name) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is still loaded in launchd", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (h *Host) Delete(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if _, err := os.Stat(h.file(name, "spec.json")); err == nil {
		if err := h.Stop(ctx, name, true); err != nil {
			return err
		}
	} else if err := h.unload(ctx, name); err != nil {
		return err
	}
	return os.RemoveAll(h.vmDir(name))
}

func (h *Host) Resize(ctx context.Context, name string, cpus, memMiB int) error {
	_, err := h.update(name, func(sp *spec) error { sp.CPUs, sp.MemMiB = cpus, memMiB; return nil })
	return err
}

func (h *Host) SetDiskBoot(ctx context.Context, name string) error {
	if _, err := h.update(name, func(sp *spec) error { sp.Boot = "disk"; return nil }); err != nil {
		return err
	}
	if b, err := os.ReadFile(h.file(name, "launchd.plist")); err != nil || strings.Contains(string(b), "usb-mass-storage") {
		return fmt.Errorf("%s: disk boot did not take — the VM still has the Talos ISO attached", name)
	}
	return nil
}

func (h *Host) SetTalosBoot(ctx context.Context, name string, b labhost.Boot, arch string) error {
	if b.ISO == "" {
		return fmt.Errorf("%s: this lab host has no Talos ISO; set it up again", name)
	}
	_, err := h.update(name, func(sp *spec) error { sp.Boot, sp.ISO, sp.Wipe = "talos", b.ISO, true; return nil })
	return err
}

func (h *Host) Autostart(ctx context.Context) error {
	specs, err := h.specs()
	if err != nil {
		return err
	}
	jobs, err := h.jobs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sp := range specs {
		if _, loaded := jobs[h.label(sp.Name)]; sp.Run && !loaded {
			errs = append(errs, h.Start(ctx, sp.Name))
		}
	}
	return errors.Join(errs...)
}

func (h *Host) state(ctx context.Context, name string) (string, error) {
	base, c := h.REST(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/vm/state", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct{ State string }
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.State, nil
}

func (h *Host) setState(ctx context.Context, name, state string) error {
	base, c := h.REST(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/vm/state", strings.NewReader(`{"state":"`+state+`"}`))
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vfkit %s: %s %s", state, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func (h *Host) answers(ctx context.Context, name string) bool {
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := h.state(c, name)
	return err == nil
}

func refused(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "no log"
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
