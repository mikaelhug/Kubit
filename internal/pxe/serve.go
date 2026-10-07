package pxe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mikaelhug/kubit/internal/factory"
	"github.com/mikaelhug/kubit/internal/fsx"
	"github.com/pin/tftp/v3"
)

type Profile struct {
	SchematicID  string
	TalosVersion string
	ExtraArgs    []string
}

func (p Profile) serves(schematic, version string) bool {
	return schematic != "" && schematic == p.SchematicID && version == p.TalosVersion && talosVersion.MatchString(version)
}

var (
	talosVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
	talosAsset   = regexp.MustCompile(`^(kernel-(amd64|arm64)|initramfs-(amd64|arm64)\.xz)$`)
)

type Server struct {
	Config
	Profile Profile
	Cache   *Cache
	Factory *factory.Client
	RunFile string
	track   *tracker
}

func (s *Server) prepare() {
	if s.track == nil {
		s.track = newTracker()
		s.Config.onDHCP = s.track.dhcp
		s.Config.onLog = s.track.logf
		s.Config.onPlainDHCP = s.track.plain
		s.Config.follow()
	}
}

const (
	keepVersions = 3
	partAge      = time.Hour
)

func (s *Server) Run(ctx context.Context) error {
	if !talosVersion.MatchString(s.Profile.TalosVersion) {
		return fmt.Errorf("Talos version %q is not a release like v1.10.3", s.Profile.TalosVersion)
	}
	s.prepare()
	if err := s.Cache.Tidy(keepVersions, s.Profile.TalosVersion, partAge); err != nil {
		s.Log.Printf("pxe: tidy cache: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.RunFile != "" {
		defer os.Remove(s.RunFile)
	}
	errc := make(chan error, 3)
	if s.HTTPOnly {
		go func() { errc <- s.serveHTTP(ctx) }()
		s.Log.Printf("pxe: HTTP only on %s (%s) :%d — no DHCP/TFTP; boot machines by hand from %s/", s.Interface, s.address(), s.HTTPPort, s.BaseURL())
	} else {
		go func() { errc <- s.ServeDHCP(ctx) }()
		go func() { errc <- s.serveTFTP(ctx) }()
		go func() { errc <- s.serveHTTP(ctx) }()
		s.Log.Printf("pxe: proxyDHCP on %s (%s), TFTP :69, HTTP :%d, Talos %s schematic %s",
			s.Interface, s.address(), s.HTTPPort, s.Profile.TalosVersion, s.Profile.SchematicID)
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

func (s *Server) serveTFTP(ctx context.Context) error {
	srv := tftp.NewServer(func(filename string, rf io.ReaderFrom) error {
		name := strings.TrimPrefix(filename, "/")
		path, err := s.Cache.IPXEBinary(ctx, name)
		if err != nil {
			s.Log.Printf("tftp: %s: %v", filename, err)
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		n, err := rf.ReadFrom(f)
		s.Log.Printf("tftp: sent %s (%d bytes)", name, n)
		return err
	}, nil)
	srv.SetTimeout(5 * time.Second)
	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()
	if err := srv.ListenAndServe(":69"); err != nil && ctx.Err() == nil {
		return fmt.Errorf("tftp :69 (needs root): %w", err)
	}
	return nil
}

func archFromIPXE(a string) string {
	switch a {
	case "x86_64", "i386", "amd64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return ""
}

func serialConsole(arch string) string {
	if arch == "arm64" {
		return "console=ttyAMA0"
	}
	return "console=ttyS0"
}

func talosKernelArgs(consoles ...string) []string {
	return slices.Concat([]string{"talos.platform=metal"}, consoles, []string{"init_on_alloc=1", "slab_nomerge", "pti=on"})
}

func (s *Server) Handler() http.Handler {
	s.prepare()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status.json", s.statusHandler)
	mux.HandleFunc("GET /boot.ipxe", func(w http.ResponseWriter, r *http.Request) {
		arch := archFromIPXE(r.URL.Query().Get("arch"))
		w.Header().Set("Content-Type", "text/plain")
		if arch == "" {
			fmt.Fprintf(w, "#!ipxe\nchain %s?arch=${buildarch}&mac=${net0/mac}\n", s.ScriptURL())
			return
		}
		mac, decision := r.URL.Query().Get("mac"), ""
		if mac != "" {
			decision = s.Config.decide(mac)
		}
		if decision == "local" {
			fmt.Fprint(w, "#!ipxe\necho Kubit: this machine boots from its own disk\nexit\n")
			s.track.logf(fmt.Sprintf("%s (%s) boots from its own disk; iPXE exits", hostOf(r.RemoteAddr), mac))
			return
		}
		base := fmt.Sprintf("%s/assets/%s/%s", s.BaseURL(), s.Profile.SchematicID, s.Profile.TalosVersion)
		args := append(talosKernelArgs("console=tty0", serialConsole(arch)), s.Profile.ExtraArgs...)
		fmt.Fprintf(w, "#!ipxe\nkernel %s/kernel-%s initrd=initramfs-%s.xz %s\ninitrd %s/initramfs-%s.xz\nboot\n",
			base, arch, arch, strings.Join(args, " "), base, arch)
		s.Log.Printf("http: boot script for %s (%s)", r.RemoteAddr, arch)
		s.track.http(hostOf(r.RemoteAddr), arch, "ipxe")
		s.track.logf(fmt.Sprintf("iPXE on %s fetched the %s boot script", hostOf(r.RemoteAddr), arch))
	})
	mux.HandleFunc("GET /assets/{schematic}/{version}/{file}", func(w http.ResponseWriter, r *http.Request) {
		schematic, version, file := r.PathValue("schematic"), r.PathValue("version"), r.PathValue("file")
		if !talosAsset.MatchString(file) || !s.Profile.serves(schematic, version) {
			http.NotFound(w, r)
			return
		}
		url := fmt.Sprintf("%s/image/%s/%s/%s", s.Factory.BaseURL(), schematic, version, file)
		path, err := s.Cache.Path(r.Context(), url)
		if err != nil {
			s.Log.Printf("http: %s: %v", url, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if strings.HasPrefix(file, "kernel-") {
			s.track.http(hostOf(r.RemoteAddr), archFromIPXE(strings.TrimSuffix(strings.TrimPrefix(file, "kernel-"), ".xz")), "kernel")
			s.track.logf(fmt.Sprintf("%s downloading %s %s", hostOf(r.RemoteAddr), version, file))
		}
		http.ServeFile(w, r, path)
	})
	return mux
}

func (s *Server) serveHTTP(ctx context.Context) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.HTTPPort))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if s.RunFile != "" {
		if err := fsx.WriteFile(s.RunFile, []byte(s.BaseURL()+"\n"), 0o644); err != nil {
			s.Log.Printf("pxe: %v", err)
		}
	}
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func hostOf(remote string) string {
	if h, _, err := net.SplitHostPort(remote); err == nil {
		return h
	}
	return remote
}
