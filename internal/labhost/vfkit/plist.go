package vfkit

import (
	"fmt"
	"strings"
	"text/template"
)

func (h *Host) args(s *spec) []string {
	d := h.vmDir(s.Name)
	a := []string{
		h.VMNetRun, "--operation-mode", "shared", "--start-address", Gateway, "--end-address", rangeEnd, "--subnet-mask", mask, "--",
		h.VFKit, "--cpus", fmt.Sprint(s.CPUs), "--memory", fmt.Sprint(s.MemMiB),
		"--bootloader", "efi,variable-store=" + d + "/efi-vars,create",
		"--device", "virtio-blk,path=" + d + "/disk.raw",
	}
	if s.DataGiB > 0 {
		a = append(a, "--device", "virtio-blk,path="+d+"/data.raw")
	}
	a = append(a,
		"--device", "virtio-net,fd=4,mac="+s.MAC,
		"--device", "virtio-serial,logFilePath="+d+"/console.log",
		"--device", "virtio-rng",
		"--restful-uri", "unix://"+h.sock(s.Name),
	)
	if s.Boot == "talos" && s.ISO != "" {
		a = append(a, "--device", "usb-mass-storage,path="+s.ISO+",readonly")
	}
	return a
}

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>{{html .Label}}</string>
  <key>ProgramArguments</key>
  <array>
{{range .Args}}    <string>{{html .}}</string>
{{end}}  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><false/>
  <key>WorkingDirectory</key><string>{{html .Dir}}</string>
  <key>StandardOutPath</key><string>{{html .Log}}</string>
  <key>StandardErrorPath</key><string>{{html .Log}}</string>
</dict>
</plist>
`))

func (h *Host) plist(s *spec) (string, error) {
	var b strings.Builder
	err := plistTmpl.Execute(&b, map[string]any{"Label": h.label(s.Name), "Args": h.args(s), "Dir": h.vmDir(s.Name), "Log": h.file(s.Name, "vfkit.log")})
	return b.String(), err
}

func (h *Host) writePlist(s *spec) (string, error) {
	p, err := h.plist(s)
	if err != nil {
		return "", err
	}
	path := h.file(s.Name, "launchd.plist")
	return path, writeAtomic(path, []byte(p))
}
