package pxe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/factory"
	"github.com/pin/tftp/v3"
)

// Profile is what every machine that network-boots gets: one Talos version and
// schematic, booting into maintenance mode on the metal platform.
type Profile struct {
	SchematicID  string
	TalosVersion string
	ExtraArgs    []string
}

type Server struct {
	Config
	Profile Profile
	Cache   *Cache
	Factory *factory.Client
}

// Run serves DHCP, TFTP and HTTP until ctx ends. DHCP and TFTP bind privileged ports.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 3)
	go func() { errc <- s.ServeDHCP(ctx) }()
	go func() { errc <- s.serveTFTP(ctx) }()
	go func() { errc <- s.serveHTTP(ctx) }()
	s.Log.Printf("pxe: proxyDHCP on %s (%s), TFTP :69, HTTP :%d, Talos %s schematic %s",
		s.Interface, s.IP, s.HTTPPort, s.Profile.TalosVersion, s.Profile.SchematicID)
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

// archFromIPXE maps iPXE's ${buildarch} to Talos/Image Factory names.
func archFromIPXE(a string) string {
	switch a {
	case "x86_64", "i386", "amd64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return ""
}

// Handler serves the iPXE script and the boot-asset cache.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boot.ipxe", func(w http.ResponseWriter, r *http.Request) {
		// iPXE substitutes ${buildarch} before requesting; a bare hit gets a chain that
		// fills it in.
		arch := archFromIPXE(r.URL.Query().Get("arch"))
		w.Header().Set("Content-Type", "text/plain")
		if arch == "" {
			fmt.Fprintf(w, "#!ipxe\nchain %s?arch=${buildarch}\n", s.ScriptURL())
			return
		}
		base := fmt.Sprintf("http://%s:%d/assets/%s/%s", s.IP, s.HTTPPort, s.Profile.SchematicID, s.Profile.TalosVersion)
		args := append([]string{"talos.platform=metal", "console=tty0", "console=ttyS0", "init_on_alloc=1", "slab_nomerge", "pti=on"}, s.Profile.ExtraArgs...)
		fmt.Fprintf(w, "#!ipxe\nkernel %s/kernel-%s initrd=initramfs-%s.xz %s\ninitrd %s/initramfs-%s.xz\nboot\n",
			base, arch, arch, strings.Join(args, " "), base, arch)
		s.Log.Printf("http: boot script for %s (%s)", r.RemoteAddr, arch)
	})
	mux.HandleFunc("GET /assets/{schematic}/{version}/{file}", func(w http.ResponseWriter, r *http.Request) {
		schematic, version, file := r.PathValue("schematic"), r.PathValue("version"), r.PathValue("file")
		if !strings.HasPrefix(file, "kernel-") && !strings.HasPrefix(file, "initramfs-") {
			http.NotFound(w, r)
			return
		}
		url := fmt.Sprintf("%s/image/%s/%s/%s", s.Factory.BaseURL, schematic, version, file)
		path, err := s.Cache.Path(r.Context(), url)
		if err != nil {
			s.Log.Printf("http: %s: %v", url, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		http.ServeFile(w, r, path)
	})
	return mux
}

func (s *Server) serveHTTP(ctx context.Context) error {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", s.HTTPPort), Handler: s.Handler()}
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

// InterfaceIPv4 returns the first IPv4 address on the named interface.
func InterfaceIPv4(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("%s has no IPv4 address", name)
}
