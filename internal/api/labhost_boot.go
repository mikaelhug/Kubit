package api

import (
	"net/http"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/labhost/libvirt"
	"github.com/mikael/kubit/internal/store"
)

func (s *Server) handleLabPostInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(libvirt.PostInstall(libvirt.PreseedParams{PostURL: r.URL.Query().Get("post")}.ProgressURL())))
}

func (s *Server) handleLabPreseed(w http.ResponseWriter, r *http.Request) {
	mac := queryMAC(r)
	m, err := s.store.GetMachine(r.Context(), mac)
	if err != nil {
		http.Error(w, "unknown machine", http.StatusNotFound)
		return
	}
	_, pub, err := s.store.SSHKey(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	var disk labhost.DiskRef
	switch {
	case m.LabHost != nil && m.LabHost.InstallDisk != nil:
		disk = *m.LabHost.InstallDisk
	case m.LabHost != nil && m.LabHost.Disk != "":
		disk = labhost.DiskRef{Key: m.LabHost.Disk, DevPath: m.LabHost.Disk}
	default:
		if inv, ok := inventoryOf(m); ok {
			if refs := diskRefs(inv); len(refs) > 0 {
				disk = refs[0]
			}
		}
	}
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = m.Arch
	}
	out, err := libvirt.Preseed(libvirt.PreseedParams{Hostname: labHostname(m), Disk: disk, PublicKey: pub, PostURL: r.URL.Query().Get("post"), Timezone: r.URL.Query().Get("tz"), Arch: arch})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(out))
}

func labBoot(m *store.Machine) (string, bool) {
	if m != nil && m.Provision && m.ProvisionKind == "labhost" && (m.LabHost == nil || m.LabHost.State == "installing") {
		return "debian", true
	}
	return "", false
}
