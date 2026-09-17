package pxe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/labhost"
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
	track   *tracker
}

// Track installs the boot tracker; Run calls it, tests may call it directly.
func (s *Server) Track() {
	if s.track == nil {
		s.track = newTracker()
		s.Config.onDHCP = s.track.dhcp
		s.Config.onLog = s.track.logf
		s.Config.onPlainDHCP = s.track.plain
	}
}

// Run serves DHCP, TFTP and HTTP until ctx ends. DHCP and TFTP bind privileged ports.
func (s *Server) Run(ctx context.Context) error {
	s.Track()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 3)
	if s.HTTPOnly {
		go func() { errc <- s.serveHTTP(ctx) }()
		s.Log.Printf("pxe: HTTP only on %s (%s) :%d — no DHCP/TFTP; boot machines by hand from http://%s:%d/", s.Interface, s.IP, s.HTTPPort, s.IP, s.HTTPPort)
	} else {
		go func() { errc <- s.ServeDHCP(ctx) }()
		go func() { errc <- s.serveTFTP(ctx) }()
		go func() { errc <- s.serveHTTP(ctx) }()
		s.Log.Printf("pxe: proxyDHCP on %s (%s), TFTP :69, HTTP :%d, Talos %s schematic %s",
			s.Interface, s.IP, s.HTTPPort, s.Profile.TalosVersion, s.Profile.SchematicID)
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
	s.Track()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status.json", s.statusHandler)
	mux.HandleFunc("GET /boot.ipxe", func(w http.ResponseWriter, r *http.Request) {
		// iPXE substitutes ${buildarch} and ${net0/mac} before requesting; a bare hit
		// gets a chain that fills them in.
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
		switch decision {
		case "debian":
			// Lab host: the Debian installer with Kubit's preseed, no Talos.
			base := fmt.Sprintf("http://%s:%d", s.IP, s.HTTPPort)
			args := labhost.KernelArgs(fmt.Sprintf("%s/labhost/%s/preseed?arch=%s", base, mac, arch), "")
			fmt.Fprintf(w, "#!ipxe\nkernel %s/assets/debian/%s/linux initrd=initrd.gz %s\ninitrd %s/assets/debian/%s/initrd.gz\nboot\n", base, arch, args, base, arch)
			s.track.http(hostOf(r.RemoteAddr), arch, "debian")
			s.track.logf(fmt.Sprintf("%s (%s) fetched the Debian installer script (lab host)", hostOf(r.RemoteAddr), mac))
			return
		case "local":
			// Second line of defence (the DHCP layer normally never offered): exit
			// iPXE so the firmware continues with the next boot device.
			fmt.Fprint(w, "#!ipxe\necho Kubit: this machine boots from its own disk\nexit\n")
			s.track.logf(fmt.Sprintf("%s (%s) boots from its own disk; iPXE exits", hostOf(r.RemoteAddr), mac))
			return
		}
		base := fmt.Sprintf("http://%s:%d/assets/%s/%s", s.IP, s.HTTPPort, s.Profile.SchematicID, s.Profile.TalosVersion)
		args := append([]string{"talos.platform=metal", "console=tty0", "console=ttyS0", "init_on_alloc=1", "slab_nomerge", "pti=on"}, s.Profile.ExtraArgs...)
		fmt.Fprintf(w, "#!ipxe\nkernel %s/kernel-%s initrd=initramfs-%s.xz %s\ninitrd %s/initramfs-%s.xz\nboot\n",
			base, arch, arch, strings.Join(args, " "), base, arch)
		s.Log.Printf("http: boot script for %s (%s)", r.RemoteAddr, arch)
		s.track.http(hostOf(r.RemoteAddr), arch, "ipxe")
		s.track.logf(fmt.Sprintf("iPXE on %s fetched the %s boot script", hostOf(r.RemoteAddr), arch))
	})
	mux.HandleFunc("GET /assets/debian/{arch}/{file}", func(w http.ResponseWriter, r *http.Request) {
		arch, file := r.PathValue("arch"), r.PathValue("file")
		if file != "linux" && file != "initrd.gz" {
			http.NotFound(w, r)
			return
		}
		path, err := s.Cache.Path(r.Context(), labhost.NetbootURL(arch, file))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if file == "linux" {
			s.track.http(hostOf(r.RemoteAddr), arch, "kernel")
		}
		s.track.logf(fmt.Sprintf("%s downloading Debian %s %s", hostOf(r.RemoteAddr), arch, file))
		http.ServeFile(w, r, path)
	})
	// The installer fetches its preseed and post-install script through this proxy
	// and reports progress the same way; only the pxe process holds the daemon's token.
	mux.HandleFunc("GET /labhost/{mac}/{file}", func(w http.ResponseWriter, r *http.Request) {
		mac, file := r.PathValue("mac"), r.PathValue("file")
		if s.KubitURL == "" || (file != "preseed" && file != "postinstall" && file != "progress") {
			http.NotFound(w, r)
			return
		}
		base := fmt.Sprintf("http://%s:%d", s.IP, s.HTTPPort)
		q := url.Values{"mac": {mac}, "post": {base + "/labhost/" + mac + "/postinstall"}, "ip": {hostOf(r.RemoteAddr)}}
		for _, k := range []string{"arch", "stage"} {
			if v := r.URL.Query().Get(k); v != "" {
				q.Set(k, v)
			}
		}
		u := fmt.Sprintf("%s/api/v1/labhost/%s?%s", strings.TrimRight(s.KubitURL, "/"), file, q.Encode())
		req, _ := http.NewRequestWithContext(r.Context(), "GET", u, nil)
		if s.KubitToken != "" {
			req.Header.Set("Authorization", "Bearer "+s.KubitToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		if file == "progress" {
			s.track.logf(fmt.Sprintf("%s (%s) installer: %s", hostOf(r.RemoteAddr), mac, r.URL.Query().Get("stage")))
		} else {
			s.track.logf(fmt.Sprintf("%s fetched %s for %s", hostOf(r.RemoteAddr), file, mac))
		}
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
		if strings.HasPrefix(file, "kernel-") {
			s.track.http(hostOf(r.RemoteAddr), archFromIPXE(strings.TrimSuffix(strings.TrimPrefix(file, "kernel-"), ".xz")), "kernel")
			s.track.logf(fmt.Sprintf("%s downloading %s %s", hostOf(r.RemoteAddr), version, file))
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

func hostOf(remote string) string {
	if h, _, err := net.SplitHostPort(remote); err == nil {
		return h
	}
	return remote
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
