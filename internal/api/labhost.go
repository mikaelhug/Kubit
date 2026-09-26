package api

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) labhostRoutes() {
	r := s.mux
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost", s.handleLabProvision)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost", s.handleLabRelease)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms", s.handleLabAddVMs)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/vms/{name}/{action}", s.handleLabVMAction)
	r.HandleFunc("PUT /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMResize)
	r.HandleFunc("DELETE /api/v1/machines/{mac}/labhost/vms/{name}", s.handleLabVMDelete)
	r.HandleFunc("GET /api/v1/labhost/preseed", s.handleLabPreseed)
	r.HandleFunc("GET /api/v1/labhost/postinstall", s.handleLabPostInstall)
	r.HandleFunc("GET /api/v1/labhost/progress", s.handleLabProgress)
	r.HandleFunc("POST /api/v1/machines", s.handleMachineAdd)
	r.HandleFunc("GET /api/v1/labhosts/local", s.handleLabLocal)
	r.HandleFunc("POST /api/v1/labhosts", s.handleLabLocalCreate)
	r.HandleFunc("GET /api/v1/machines/{mac}/labhost/samples", s.handleLabSamples)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/check", s.handleLabCheck)
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/update", s.handleLabMaintain(true))
	r.HandleFunc("POST /api/v1/machines/{mac}/labhost/reboot", s.handleLabMaintain(false))
}

func labHostname(m *store.Machine) string {
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

func (s *Server) reconcileLabHosts(ctx context.Context) {
	rows, err := s.store.ListNodes(ctx, "")
	if err != nil {
		return
	}
	for i := range rows {
		host := &rows[i]
		if host.LabHost == nil {
			continue
		}
		switch host.LabHost.State {
		case "installing", "setup":
			if err := s.releaseLabHost(ctx, host); err != nil {
				log.Printf("lab host %s: release after restart: %v", host.MAC, err)
			}
		case "updating":
			_ = s.store.UpdateLabHost(ctx, host.MAC, func(lh *store.LabHost) { lh.State = "ready" })
		case "ready":
			if host.LabHost.Driver == labhost.DriverVFKit {
				go s.labAutostart(s.ctx, host)
			}
		}
	}
}

func uniq(v ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range v {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (s *Server) labHostOf(w http.ResponseWriter, r *http.Request) (*store.Machine, bool) {
	host, err := s.store.GetMachine(r.Context(), pathMAC(r))
	if err != nil || host.LabHost == nil {
		writeErr(w, &statusError{http.StatusNotFound, "not a lab host"})
		return nil, false
	}
	return host, true
}

func (s *Server) labDriver(ctx context.Context, hostMAC string) string {
	if hostMAC == "" {
		return ""
	}
	if h, err := s.store.GetMachine(ctx, hostMAC); err == nil && h.LabHost != nil {
		return h.LabHost.Driver
	}
	return ""
}
