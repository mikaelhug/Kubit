package libvirt

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mikael/kubit/internal/labhost"
)

func firmware(arch string) (code, vars string) {
	if arch == "arm64" {
		return "/usr/share/AAVMF/AAVMF_CODE.fd", "/usr/share/AAVMF/AAVMF_VARS.fd"
	}
	return "/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"
}

func vmCmdline(arch string) string {
	return strings.Join(labhost.TalosKernelArgs(labhost.SerialConsole(arch), "console=tty0"), " ")
}

var domainTmpl = template.Must(template.New("domain").Parse(`<domain type='{{.Type}}'>
  <name>{{.Name}}</name>
  <memory unit='MiB'>{{.MemMiB}}</memory>
  <vcpu>{{.CPUs}}</vcpu>
  <os>
    <type arch='{{.QemuArch}}' machine='{{.Machine}}'>hvm</type>
    <loader readonly='yes' type='pflash'>{{.Loader}}</loader>
    <nvram template='{{.Vars}}'>{{.NVRAM}}</nvram>
{{if .Kernel}}    <kernel>{{.Kernel}}</kernel>
    <initrd>{{.Initrd}}</initrd>
    <cmdline>{{.Cmdline}}</cmdline>
{{else}}    <boot dev='hd'/>
{{end}}  </os>
  <features><acpi/><apic/></features>
  <cpu mode='{{.CPUMode}}'/>
  <clock offset='utc'/>
  <on_reboot>restart</on_reboot>
  <devices>
    <emulator>{{.Emulator}}</emulator>
{{range .Disks}}{{if eq .Type "block"}}    <disk type='block' device='disk'>
      <driver name='qemu' type='raw' cache='none' io='native' discard='unmap'/>
      <source dev='{{.Path}}'/>
{{else}}    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='{{.Path}}'/>
{{end}}      <target dev='{{.Target}}' bus='virtio'/>
    </disk>
{{end}}{{if .Routed}}    <interface type='network'>
      <source network='kubit'/>
{{else}}    <interface type='bridge'>
      <source bridge='{{.Bridge}}'/>
{{end}}      <mac address='{{.MAC}}'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'><target port='0'/></serial>
    <console type='pty'><target type='serial' port='0'/></console>
    <rng model='virtio'><backend model='random'>/dev/urandom</backend></rng>
  </devices>
</domain>
`))

func DomainXML(s labhost.VMSpec) (string, error) {
	qarch, machine, emulator := "x86_64", "q35", "/usr/bin/qemu-system-x86_64"
	if s.Arch == "arm64" {
		qarch, machine, emulator = "aarch64", "virt", "/usr/bin/qemu-system-aarch64"
	}
	bridge := s.Bridge
	if bridge == "" {
		bridge = "br0"
	}
	loader, vars := firmware(s.Arch)
	typ, cpu := "kvm", "host-passthrough"
	if s.TCG {
		typ, cpu = "qemu", "maximum"
	}
	disks := vmDisks(s)
	for _, d := range disks {
		if d.Type == "block" && !labhost.SafeDevice(d.Path) || !labhost.ValidPoolName(orSystem(d.Pool)) {
			return "", fmt.Errorf("%s: bad disk %s", s.Name, d.Path)
		}
	}
	data := struct {
		labhost.VMSpec
		QemuArch, Machine, Emulator, Bridge, Type, CPUMode, Loader, Vars, NVRAM, Cmdline string
		Disks                                                                            []domDisk
	}{s, qarch, machine, emulator, bridge, typ, cpu, loader, vars, vmDir + "/" + s.Name + ".nvram", vmCmdline(s.Arch), disks}
	var b bytes.Buffer
	if err := domainTmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

type domDisk struct {
	Type, Path, Target, Pool string
	GiB                      int
}

func vmDisks(s labhost.VMSpec) []domDisk {
	disk := func(p labhost.Placement, suffix, target string, gib int) domDisk {
		if p.Device != "" {
			return domDisk{Type: "block", Path: p.Device, Target: target}
		}
		return domDisk{Type: "file", Path: imagePath(p.Pool, s.Name, suffix), Target: target, Pool: p.Pool, GiB: gib}
	}
	out := []domDisk{disk(s.System, "", "vda", s.DiskGiB)}
	if s.Data.Device != "" || s.DataGiB > 0 {
		out = append(out, disk(s.Data, "-data", "vdb", s.DataGiB))
	}
	return out
}

func orSystem(pool string) string {
	if pool == "" {
		return labhost.SystemPool
	}
	return pool
}

func (c *Client) Define(ctx context.Context, s labhost.VMSpec) error {
	xml, err := DomainXML(s)
	if err != nil {
		return err
	}
	defer labhost.ForgetCapacity(c.capacityKey())
	for _, d := range vmDisks(s) {
		var err error
		if d.Type == "block" {
			err = c.wipe(ctx, d.Path)
		} else {
			err = c.qcow2(ctx, d.Path, d.GiB)
		}
		if err != nil {
			return err
		}
	}
	if err := c.defineXML(ctx, s.Name, xml); err != nil {
		return err
	}
	if _, err := c.Run(ctx, "virsh autostart "+s.Name+" >/dev/null"); err != nil {
		return err
	}
	return c.Start(ctx, s.Name)
}

const RoutedSubnet = "192.168.123.0/24"

const routedNetwork = `<network>
  <name>kubit</name>
  <forward mode='open'/>
  <bridge name='kubitbr0' stp='off' delay='0'/>
  <ip address='192.168.123.1' netmask='255.255.255.0'>
    <dhcp><range start='192.168.123.100' end='192.168.123.200'/></dhcp>
  </ip>
</network>
`

const routedUnit = `[Unit]
Description=Kubit routed VM network: forwarding and egress masquerade
After=libvirtd.service network-online.target
Wants=network-online.target
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'sysctl -qw net.ipv4.ip_forward=1; nft list table ip kubit >/dev/null 2>&1 || nft add table ip kubit; nft list chain ip kubit post >/dev/null 2>&1 || nft add chain ip kubit post "{ type nat hook postrouting priority 100 ; }"; nft flush chain ip kubit post; nft add rule ip kubit post ip saddr 192.168.123.0/24 ip daddr != 192.168.123.0/24 masquerade'
[Install]
WantedBy=multi-user.target
`

func (c *Client) EnsureRouted(ctx context.Context) error {
	if err := c.Put(ctx, "/var/lib/kubit/network.xml", []byte(routedNetwork), "644"); err != nil {
		return err
	}
	if err := c.Put(ctx, "/etc/systemd/system/kubit-vmnet.service", []byte(routedUnit), "644"); err != nil {
		return err
	}
	_, err := c.Run(ctx, "virsh net-info kubit >/dev/null 2>&1 || virsh net-define /var/lib/kubit/network.xml >/dev/null; virsh net-autostart kubit >/dev/null; virsh net-info kubit | grep -q 'Active:.*yes' || virsh net-start kubit >/dev/null; systemctl daemon-reload; systemctl enable --now kubit-vmnet.service >/dev/null 2>&1; nft list chain ip kubit post | grep -q masquerade")
	return err
}

func qcow2Script(path string, gib int) string {
	return fmt.Sprintf(`d=$(dirname %[1]s); [ "$d" = %[2]s ] || mountpoint -q "$d" || { echo "$d is not mounted" >&2; exit 1; }; [ -f %[1]s ] || qemu-img create -q -f qcow2 %[1]s %[3]dG`, path, vmDir, gib)
}

func (c *Client) qcow2(ctx context.Context, path string, gib int) error {
	_, err := c.Run(ctx, qcow2Script(path, gib))
	return err
}

func (c *Client) defineXML(ctx context.Context, name, xml string) error {
	if err := c.Put(ctx, vmDir+"/"+name+".xml", []byte(xml), "644"); err != nil {
		return err
	}
	_, err := c.Run(ctx, "virsh define "+vmDir+"/"+name+".xml >/dev/null")
	return err
}

func (c *Client) redefine(ctx context.Context, name string, edit func(xml string) (string, error), wantKernel bool, stuck string) error {
	out, err := c.Run(ctx, "virsh dumpxml "+name+" --inactive")
	if err != nil {
		return err
	}
	xml, err := edit(out)
	if err != nil {
		return err
	}
	if err := c.defineXML(ctx, name, xml); err != nil {
		return err
	}
	if after, err := c.Run(ctx, "virsh dumpxml "+name+" --inactive"); err == nil && strings.Contains(after, "<kernel>") != wantKernel {
		return fmt.Errorf("%s: %s", name, stuck)
	}
	return nil
}

func (c *Client) SetDiskBoot(ctx context.Context, name string) error {
	return c.redefine(ctx, name, func(out string) (string, error) {
		xml := kernelBlock.ReplaceAllString(out, "<boot dev='hd'/>")
		if xml == out {
			return "", fmt.Errorf("%s: no Talos kernel block found in the domain XML — cannot switch to disk boot (libvirt XML format may have changed)", name)
		}
		return xml, nil
	}, false, "disk boot did not take — the domain still has a Talos kernel after redefine")
}

var kernelBlock = regexp.MustCompile(`(?s)<kernel>.*?</kernel>\s*<initrd>.*?</initrd>\s*<cmdline>.*?</cmdline>`)

func (c *Client) SetTalosBoot(ctx context.Context, name string, b labhost.Boot, arch string) error {
	return c.redefine(ctx, name, func(out string) (string, error) {
		block := fmt.Sprintf("<kernel>%s</kernel>\n    <initrd>%s</initrd>\n    <cmdline>%s</cmdline>", b.Kernel, b.Initrd, vmCmdline(arch))
		xml := strings.Replace(out, "<boot dev='hd'/>", block, 1)
		if xml == out {
			return "", fmt.Errorf("%s: no <boot dev='hd'/> found in the domain XML — cannot switch to Talos boot (libvirt XML format may have changed)", name)
		}
		return xml, nil
	}, true, "Talos boot did not take — the domain has no kernel after redefine")
}

func (c *Client) Start(ctx context.Context, name string) error {
	_, err := c.Run(ctx, "virsh start "+name+" >/dev/null")
	if err == nil {
		return nil
	}
	if !connDropped(err) {
		if st, e := c.domState(ctx, name); e == nil && st == "running" {
			return nil
		}
		return err
	}
	if err := c.reconnect(ctx, 45*time.Second); err != nil {
		return fmt.Errorf("starting %s reset the host's network and it did not come back: %w", name, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if st, e := c.domState(ctx, name); e == nil && st == "running" {
			return nil
		} else if e != nil && connDropped(e) {
			_ = c.reconnect(ctx, 45*time.Second)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not reach running after the host's network reset", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) domState(ctx context.Context, name string) (string, error) {
	out, err := c.Run(ctx, "virsh domstate "+name)
	return strings.TrimSpace(out), err
}

func (c *Client) Stop(ctx context.Context, name string, force bool) error {
	if force {
		_, err := c.Run(ctx, "virsh destroy "+name+" >/dev/null 2>&1 || true")
		return err
	}
	_, err := c.Run(ctx, "virsh shutdown "+name+" >/dev/null 2>&1 || true")
	return err
}

func deleteScript(name string) string {
	return strings.NewReplacer("NAME", name, "VMDIR", vmDir, "POOLS", poolsDir).Replace(`blk=$(virsh domblklist NAME --details --inactive 2>/dev/null | awk '$1=="block" && $2=="disk"{print $4}'); ` +
		`virsh destroy NAME >/dev/null 2>&1; virsh undefine NAME --nvram >/dev/null 2>&1 || virsh undefine NAME >/dev/null 2>&1; ` +
		`rm -f VMDIR/NAME.qcow2 VMDIR/NAME-data.qcow2 POOLS/*/NAME.qcow2 POOLS/*/NAME-data.qcow2 VMDIR/NAME.xml VMDIR/NAME.nvram; ` +
		`if virsh dominfo NAME >/dev/null 2>&1; then echo 'domain still defined after undefine' >&2; exit 1; fi; ` +
		wipeFn + `; for b in $blk; do case "$b" in /dev/*) wipe "$b" || echo "could not wipe $b" >&2;; esac; done; true`)
}

func (c *Client) Delete(ctx context.Context, name string) error {
	defer labhost.ForgetCapacity(c.capacityKey())
	_, err := c.Run(ctx, deleteScript(name))
	return err
}

func (c *Client) Resize(ctx context.Context, name string, cpus, memMiB int) error {
	_, err := c.Run(ctx, fmt.Sprintf("virsh setmaxmem %s %dM --config && virsh setmem %s %dM --config && virsh setvcpus %s %d --config --maximum && virsh setvcpus %s %d --config", name, memMiB, name, memMiB, name, cpus, name, cpus))
	return err
}

const listScript = `for d in $(virsh list --all --name); do [ -z "$d" ] && continue; st=$(virsh domstate $d | head -1); ` +
	`ip=$( (virsh domifaddr $d --source lease 2>/dev/null; virsh domifaddr $d --source arp 2>/dev/null) | awk '/ipv4/{print $4}' | head -1 | cut -d/ -f1); ` +
	`echo "@@vm $d|$st|$ip"; virsh dumpxml $d --inactive; ` +
	`virsh domblklist $d --details --inactive 2>/dev/null | awk 'NR>2 && $2=="disk"{print $1, $4}' | while read t src; do ` +
	`if [ "$t" = block ]; then b=$(blockdev --getsize64 "$src" 2>/dev/null); else b=$(qemu-img info -U "$src" 2>/dev/null | grep '^virtual size' | grep -o '([0-9]* bytes)' | tr -dc 0-9); fi; ` +
	`echo "@@size $src|$b"; done; done`

type domainDoc struct {
	Memory struct {
		Unit  string `xml:"unit,attr"`
		Value int64  `xml:",chardata"`
	} `xml:"memory"`
	VCPU int `xml:"vcpu"`
	OS   struct {
		Kernel string `xml:"kernel"`
	} `xml:"os"`
	Devices struct {
		Disks []struct {
			Device string `xml:"device,attr"`
			Source struct {
				File string `xml:"file,attr"`
				Dev  string `xml:"dev,attr"`
			} `xml:"source"`
			Target struct {
				Dev string `xml:"dev,attr"`
			} `xml:"target"`
		} `xml:"disk"`
		Interfaces []struct {
			MAC struct {
				Address string `xml:"address,attr"`
			} `xml:"mac"`
		} `xml:"interface"`
	} `xml:"devices"`
}

func parseList(out string) []labhost.VM {
	var vms []labhost.VM
	var head string
	var doc strings.Builder
	sizes := map[string]int64{}
	flush := func() {
		if head == "" {
			return
		}
		f := strings.Split(head, "|")
		for len(f) < 3 {
			f = append(f, "")
		}
		vm := labhost.VM{Name: f[0], State: f[1], IP: f[2], Boot: "disk"}
		var d domainDoc
		if err := xml.Unmarshal([]byte(doc.String()), &d); err == nil {
			mem := d.Memory.Value
			switch d.Memory.Unit {
			case "", "KiB", "k":
				mem /= 1024
			case "GiB", "G":
				mem *= 1024
			case "b", "bytes":
				mem >>= 20
			}
			vm.MemMiB, vm.CPUs = int(mem), d.VCPU
			if strings.TrimSpace(d.OS.Kernel) != "" {
				vm.Boot = "talos"
			}
			if len(d.Devices.Interfaces) > 0 {
				vm.MAC = d.Devices.Interfaces[0].MAC.Address
			}
			for _, disk := range d.Devices.Disks {
				if disk.Device != "" && disk.Device != "disk" {
					continue
				}
				vd := labhost.VMDisk{Target: disk.Target.Dev}
				src := disk.Source.File
				if disk.Source.Dev != "" {
					src, vd.Device = disk.Source.Dev, disk.Source.Dev
				} else {
					vd.Pool = poolOfImage(src)
				}
				vd.GiB = int(sizes[src] >> 30)
				switch vd.Target {
				case "vda":
					vm.DiskGiB = vd.GiB
				case "vdb":
					vm.DataGiB = vd.GiB
				}
				vm.Disks = append(vm.Disks, vd)
			}
		}
		vms = append(vms, vm)
		head = ""
		doc.Reset()
		clear(sizes)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "@@vm "):
			flush()
			head = strings.TrimPrefix(line, "@@vm ")
		case strings.HasPrefix(line, "@@size "):
			src, b, _ := strings.Cut(strings.TrimPrefix(line, "@@size "), "|")
			n, _ := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
			sizes[src] = n
		case head != "":
			doc.WriteString(line)
			doc.WriteByte('\n')
		}
	}
	flush()
	return vms
}

func (c *Client) List(ctx context.Context) ([]labhost.VM, error) {
	out, err := c.Run(ctx, listScript)
	if err != nil {
		return nil, err
	}
	return parseList(out), nil
}
