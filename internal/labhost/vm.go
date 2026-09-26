package labhost

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
)

type VMSpec struct {
	Name    string `json:"name"`
	MAC     string `json:"mac"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB,omitempty"`
	Kernel  string `json:"-"`
	Initrd  string `json:"-"`
	ISO     string `json:"-"`
	Arch    string `json:"-"`
	Bridge  string `json:"-"`
	Routed  bool   `json:"-"`
	TCG     bool   `json:"-"`
}

func firmware(arch string) (code, vars string) {
	if arch == "arm64" {
		return "/usr/share/AAVMF/AAVMF_CODE.fd", "/usr/share/AAVMF/AAVMF_VARS.fd"
	}
	return "/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"
}

type VM struct {
	Name    string `json:"name"`
	MAC     string `json:"mac"`
	State   string `json:"state"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB,omitempty"`
	Boot    string `json:"boot"`
	IP      string `json:"ip,omitempty"`
}

func MAC(host, n int) string { return fmt.Sprintf("52:54:00:6b:%02x:%02x", host&0xff, n&0xff) }

func serialConsole(arch string) string {
	if arch == "arm64" {
		return "console=ttyAMA0"
	}
	return "console=ttyS0"
}

func TalosKernelArgs(consoles ...string) []string {
	return slices.Concat([]string{"talos.platform=metal"}, consoles, []string{"init_on_alloc=1", "slab_nomerge", "pti=on"})
}

func vmCmdline(arch string) string {
	return strings.Join(TalosKernelArgs(serialConsole(arch), "console=tty0"), " ")
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
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='{{.Disk}}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
{{if .Data}}    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='{{.Data}}'/>
      <target dev='vdb' bus='virtio'/>
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

func DomainXML(s VMSpec) (string, error) {
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
	data := struct {
		VMSpec
		QemuArch, Machine, Emulator, Disk, Data, Bridge, Type, CPUMode, Loader, Vars, NVRAM, Cmdline string
	}{s, qarch, machine, emulator, diskPath(s.Name), "", bridge, typ, cpu, loader, vars, vmDir + "/" + s.Name + ".nvram", vmCmdline(s.Arch)}
	if s.DataGiB > 0 {
		data.Data = dataPath(s.Name)
	}
	var b bytes.Buffer
	if err := domainTmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func diskPath(name string) string { return vmDir + "/" + name + ".qcow2" }
func dataPath(name string) string { return vmDir + "/" + name + "-data.qcow2" }

func (c *Client) Define(ctx context.Context, s VMSpec) error {
	xml, err := DomainXML(s)
	if err != nil {
		return err
	}
	if err := c.qcow2(ctx, diskPath(s.Name), s.DiskGiB); err != nil {
		return err
	}
	if s.DataGiB > 0 {
		if err := c.qcow2(ctx, dataPath(s.Name), s.DataGiB); err != nil {
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

func (c *Client) qcow2(ctx context.Context, path string, gib int) error {
	_, err := c.Run(ctx, fmt.Sprintf("[ -f %s ] || qemu-img create -q -f qcow2 %s %dG", path, path, gib))
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

func (c *Client) SetTalosBoot(ctx context.Context, name string, b Boot, arch string) error {
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

func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.Run(ctx, fmt.Sprintf("virsh destroy %s >/dev/null 2>&1; virsh undefine %s --nvram >/dev/null 2>&1 || virsh undefine %s >/dev/null 2>&1; rm -f %s %s %s/%s.xml %s/%s.nvram; if virsh dominfo %s >/dev/null 2>&1; then echo 'domain still defined after undefine' >&2; exit 1; fi", name, name, name, diskPath(name), dataPath(name), vmDir, name, vmDir, name, name))
	return err
}

func (c *Client) Resize(ctx context.Context, name string, cpus, memMiB int) error {
	_, err := c.Run(ctx, fmt.Sprintf("virsh setmaxmem %s %dM --config && virsh setmem %s %dM --config && virsh setvcpus %s %d --config --maximum && virsh setvcpus %s %d --config", name, memMiB, name, memMiB, name, cpus, name, cpus))
	return err
}

func (c *Client) List(ctx context.Context) ([]VM, error) {
	out, err := c.Run(ctx, `for d in $(virsh list --all --name); do [ -z "$d" ] && continue; st=$(virsh domstate $d | head -1); x=$(virsh dumpxml $d --inactive); mac=$(echo "$x" | grep -o "mac address='[^']*'" | head -1 | cut -d"'" -f2); mem=$(echo "$x" | grep -o "<memory unit='[A-Za-z]*'>[0-9]*" | grep -o "[0-9]*$"); unit=$(echo "$x" | grep -o "<memory unit='[A-Za-z]*'" | cut -d"'" -f2); cpu=$(echo "$x" | grep -o "<vcpu[^>]*>[0-9]*" | grep -o "[0-9]*$"); boot=$(echo "$x" | grep -q "<kernel>" && echo talos || echo disk); disk=$(qemu-img info -U `+vmDir+`/$d.qcow2 2>/dev/null | grep '^virtual size' | grep -o '([0-9]* bytes)' | tr -dc 0-9); data=$(qemu-img info -U `+vmDir+`/$d-data.qcow2 2>/dev/null | grep '^virtual size' | grep -o '([0-9]* bytes)' | tr -dc 0-9); ip=$( (virsh domifaddr $d --source lease 2>/dev/null; virsh domifaddr $d --source arp 2>/dev/null) | awk '/ipv4/{print $4}' | head -1 | cut -d/ -f1); echo "$d|$st|$mac|$mem|$unit|$cpu|$boot|$disk|$ip|$data"; done`)
	if err != nil {
		return nil, err
	}
	var vms []VM
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "|")
		if len(f) < 9 || f[0] == "" {
			continue
		}
		vm := VM{Name: f[0], State: f[1], MAC: f[2], Boot: f[6], IP: f[8]}
		mem, _ := strconv.Atoi(f[3])
		switch f[4] {
		case "KiB":
			mem /= 1024
		case "GiB":
			mem *= 1024
		}
		vm.MemMiB = mem
		vm.CPUs, _ = strconv.Atoi(f[5])
		if b, err := strconv.ParseInt(f[7], 10, 64); err == nil {
			vm.DiskGiB = int(b >> 30)
		}
		if len(f) > 9 {
			if b, err := strconv.ParseInt(f[9], 10, 64); err == nil {
				vm.DataGiB = int(b >> 30)
			}
		}
		vms = append(vms, vm)
	}
	return vms, nil
}
