package vfkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
)

const (
	Subnet    = "192.168.105.0/24"
	Gateway   = "192.168.105.1"
	rangeEnd  = "192.168.105.100"
	mask      = "255.255.255.0"
	labelBase = "dev.kubit.vm."
	bootDir   = "boot"
	maxSock   = 103
)

type Runner func(ctx context.Context, name string, args ...string) (string, error)

type Host struct {
	Dir      string
	Domain   string
	Leases   string
	VFKit    string
	VMNetRun string
	Run      Runner
	HTTP     *http.Client
	REST     func(name string) (string, *http.Client)
}

var goos = runtime.GOOS

func New(home string) (*Host, error) {
	if goos != "darwin" {
		return nil, errors.New("VMs on this machine need macOS")
	}
	h := &Host{
		Dir:      filepath.Join(home, "vms"),
		Domain:   "gui/" + strconv.Itoa(os.Getuid()),
		Leases:   "/var/db/dhcpd_leases",
		VFKit:    findTool("vfkit", "/opt/homebrew/bin/vfkit", "/usr/local/bin/vfkit"),
		VMNetRun: findTool("", "/opt/homebrew/opt/vmnet-helper/libexec/vmnet-run", "/usr/local/opt/vmnet-helper/libexec/vmnet-run"),
		Run:      run,
		HTTP:     http.DefaultClient,
	}
	h.REST = h.unixREST
	return h, nil
}

func (h *Host) sock(name string) string { return h.file(name, "rest.sock") }

func (h *Host) unixREST(name string) (string, *http.Client) {
	path := h.sock(name)
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
	return "http://vfkit", &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
}

func (h *Host) Close() error { return nil }

func findTool(name string, paths ...string) string {
	if name != "" {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return paths[0]
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (h *Host) vmDir(name string) string { return filepath.Join(h.Dir, name) }

func (h *Host) file(name, f string) string { return filepath.Join(h.Dir, name, f) }

func (h *Host) prefix() string {
	sum := sha256.Sum256([]byte(h.Dir))
	return labelBase + hex.EncodeToString(sum[:4]) + "."
}

func (h *Host) label(name string) string { return h.prefix() + name }

var (
	_ labhost.Driver      = (*Host)(nil)
	_ labhost.Identity    = (*Host)(nil)
	_ labhost.Autostarter = (*Host)(nil)
)
