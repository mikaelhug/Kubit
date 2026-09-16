package labhost

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

// VMSpec sizes one Talos VM.
type VMSpec struct {
	Name    string `json:"name"`
	MAC     string `json:"mac"`
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	// DataGiB adds a second thin disk (vdb) the cluster can claim as a data volume.
	DataGiB int `json:"dataGiB,omitempty"`
	// Kernel/Initrd set = boot Talos maintenance mode from RAM; empty = boot from disk.
	Kernel string `json:"-"`
	Initrd string `json:"-"`
	Arch   string `json:"-"`
	Bridge string `json:"-"`
	// Routed puts the VM on Kubit's own libvirt network (RoutedSubnet, DHCP from
	// dnsmasq, egress masqueraded by the host) instead of the LAN bridge: for hosts
	// whose uplink drops frames from other MACs (Wi-Fi, port security, a VM under
	// vmnet). Kubit's host then needs a route to RoutedSubnet via the lab host.
	Routed bool `json:"-"`
	// TCG runs the VM under software emulation when the host has no /dev/kvm (dev
	// harnesses without nested virtualisation); slow, never the default.
	TCG bool `json:"-"`
}

// Firmware is the UEFI code image and variable template libvirt boots the VM with.
// UEFI on both arches so Talos's installed sd-boot is what runs after SetDiskBoot.
func Firmware(arch string) (code, vars string) {
	if arch == "arm64" {
		return "/usr/share/AAVMF/AAVMF_CODE.fd", "/usr/share/AAVMF/AAVMF_VARS.fd"
	}
	return "/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"
}

// VM is what the host reports about a defined VM.
type VM struct {
	Name    string `json:"name"`
	MAC     string `json:"mac"`
	State   string `json:"state"` // running | shut off | paused | …
	CPUs    int    `json:"cpus"`
	MemMiB  int    `json:"memMiB"`
	DiskGiB int    `json:"diskGiB"`
	DataGiB int    `json:"dataGiB,omitempty"`
	Boot    string `json:"boot"` // talos | disk
	IP      string `json:"ip,omitempty"`
}

// MAC gives VM n on lab host h a stable, recognisable address (locally administered).
func MAC(host, n int) string { return fmt.Sprintf("52:54:00:6b:%02x:%02x", host&0xff, n&0xff) }

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
    <cmdline>talos.platform=metal console=ttyS0 console=tty0 init_on_alloc=1 slab_nomerge pti=on</cmdline>
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

// DomainXML renders the libvirt definition; Talos boots from kernel/initrd until the
// caller switches the VM to its disk.
func DomainXML(s VMSpec) (string, error) {
	qarch, machine, emulator := "x86_64", "q35", "/usr/bin/qemu-system-x86_64"
	if s.Arch == "arm64" {
		qarch, machine, emulator = "aarch64", "virt", "/usr/bin/qemu-system-aarch64"
	}
	bridge := s.Bridge
	if bridge == "" {
		bridge = "br0"
	}
	loader, vars := Firmware(s.Arch)
	typ, cpu := "kvm", "host-passthrough"
	if s.TCG {
		typ, cpu = "qemu", "maximum"
	}
	data := struct {
		VMSpec
		QemuArch, Machine, Emulator, Disk, Data, Bridge, Type, CPUMode, Loader, Vars, NVRAM string
	}{s, qarch, machine, emulator, DiskPath(s.Name), "", bridge, typ, cpu, loader, vars, VMDir + "/" + s.Name + ".nvram"}
	if s.DataGiB > 0 {
		data.Data = DataPath(s.Name)
	}
	var b bytes.Buffer
	if err := domainTmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// DiskPath and DataPath are the VM's thin qcow2 images on the host.
func DiskPath(name string) string { return VMDir + "/" + name + ".qcow2" }
func DataPath(name string) string { return VMDir + "/" + name + "-data.qcow2" }

// Define creates the disks (thin qcow2) and the domain, and starts it.
func (c *Client) Define(ctx context.Context, s VMSpec) error {
	xml, err := DomainXML(s)
	if err != nil {
		return err
	}
	disk := DiskPath(s.Name)
	if _, err := c.Run(ctx, fmt.Sprintf("[ -f %s ] || qemu-img create -q -f qcow2 %s %dG", disk, disk, s.DiskGiB)); err != nil {
		return err
	}
	if s.DataGiB > 0 {
		data := DataPath(s.Name)
		if _, err := c.Run(ctx, fmt.Sprintf("[ -f %s ] || qemu-img create -q -f qcow2 %s %dG", data, data, s.DataGiB)); err != nil {
			return err
		}
	}
	if err := c.Put(ctx, VMDir+"/"+s.Name+".xml", []byte(xml), "644"); err != nil {
		return err
	}
	if _, err := c.Run(ctx, "virsh define "+VMDir+"/"+s.Name+".xml >/dev/null"); err != nil {
		return err
	}
	// Autostart so a host reboot (updates, power loss) brings the lab back by itself.
	if _, err := c.Run(ctx, "virsh autostart "+s.Name+" >/dev/null"); err != nil {
		return err
	}
	_, err = c.Run(ctx, "virsh start "+s.Name+" >/dev/null")
	return err
}

// RoutedSubnet is Kubit's libvirt network for routed VMs; the host is .1.
const RoutedSubnet = "192.168.123.0/24"

// routedNetwork is an "open" libvirt network: dnsmasq for DHCP, no firewall rules of
// libvirt's own (its NAT mode rejects new inbound connections, which Kubit needs to
// reach the VMs). Forwarding and egress masquerade come from kubit-vmnet.service.
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

// EnsureRouted defines and starts the kubit network and the forwarding unit.
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

// SetDiskBoot redefines the VM to boot from its disk (after Talos installed itself);
// takes effect at the VM's next boot, which Talos triggers after the install.
func (c *Client) SetDiskBoot(ctx context.Context, name string) error {
	out, err := c.Run(ctx, "virsh dumpxml "+name+" --inactive")
	if err != nil {
		return err
	}
	xml := kernelBlock.ReplaceAllString(out, "<boot dev='hd'/>")
	if err := c.Put(ctx, VMDir+"/"+name+".xml", []byte(xml), "644"); err != nil {
		return err
	}
	_, err = c.Run(ctx, "virsh define "+VMDir+"/"+name+".xml >/dev/null")
	return err
}

var kernelBlock = regexp.MustCompile(`(?s)<kernel>.*?</kernel>\s*<initrd>.*?</initrd>\s*<cmdline>.*?</cmdline>`)

// SetTalosBoot puts a VM back on the maintenance-mode kernel (re-provisioning).
func (c *Client) SetTalosBoot(ctx context.Context, name, kernel, initrd string) error {
	out, err := c.Run(ctx, "virsh dumpxml "+name+" --inactive")
	if err != nil {
		return err
	}
	block := fmt.Sprintf("<kernel>%s</kernel>\n    <initrd>%s</initrd>\n    <cmdline>talos.platform=metal console=ttyS0 console=tty0</cmdline>", kernel, initrd)
	xml := strings.Replace(out, "<boot dev='hd'/>", block, 1)
	if err := c.Put(ctx, VMDir+"/"+name+".xml", []byte(xml), "644"); err != nil {
		return err
	}
	_, err = c.Run(ctx, "virsh define "+VMDir+"/"+name+".xml >/dev/null")
	return err
}

func (c *Client) Start(ctx context.Context, name string) error {
	_, err := c.Run(ctx, "virsh start "+name+" >/dev/null || virsh domstate "+name+" | grep -q running")
	return err
}

func (c *Client) Stop(ctx context.Context, name string, force bool) error {
	if force {
		_, err := c.Run(ctx, "virsh destroy "+name+" >/dev/null 2>&1 || true")
		return err
	}
	_, err := c.Run(ctx, "virsh shutdown "+name+" >/dev/null 2>&1 || true")
	return err
}

// Delete destroys, undefines and removes the disk.
func (c *Client) Delete(ctx context.Context, name string) error {
	_, err := c.Run(ctx, fmt.Sprintf("virsh destroy %s >/dev/null 2>&1; virsh undefine %s --nvram >/dev/null 2>&1 || virsh undefine %s >/dev/null 2>&1; rm -f %s %s %s/%s.xml %s/%s.nvram", name, name, name, DiskPath(name), DataPath(name), VMDir, name, VMDir, name))
	return err
}

// Resize changes vCPUs and memory (applied on next boot).
func (c *Client) Resize(ctx context.Context, name string, cpus, memMiB int) error {
	_, err := c.Run(ctx, fmt.Sprintf("virsh setmaxmem %s %dM --config && virsh setmem %s %dM --config && virsh setvcpus %s %d --config --maximum && virsh setvcpus %s %d --config", name, memMiB, name, memMiB, name, cpus, name, cpus))
	return err
}

// List reports every VM defined under Kubit's naming with its state and lease.
func (c *Client) List(ctx context.Context) ([]VM, error) {
	out, err := c.Run(ctx, `for d in $(virsh list --all --name); do [ -z "$d" ] && continue; st=$(virsh domstate $d | head -1); x=$(virsh dumpxml $d --inactive); mac=$(echo "$x" | grep -o "mac address='[^']*'" | head -1 | cut -d"'" -f2); mem=$(echo "$x" | grep -o "<memory unit='[A-Za-z]*'>[0-9]*" | grep -o "[0-9]*$"); unit=$(echo "$x" | grep -o "<memory unit='[A-Za-z]*'" | cut -d"'" -f2); cpu=$(echo "$x" | grep -o "<vcpu[^>]*>[0-9]*" | grep -o "[0-9]*$"); boot=$(echo "$x" | grep -q "<kernel>" && echo talos || echo disk); disk=$(qemu-img info --output=json `+VMDir+`/$d.qcow2 2>/dev/null | grep -o '"virtual-size": [0-9]*' | grep -o '[0-9]*$'); data=$(qemu-img info --output=json `+VMDir+`/$d-data.qcow2 2>/dev/null | grep -o '"virtual-size": [0-9]*' | grep -o '[0-9]*$'); ip=$( (virsh domifaddr $d --source lease 2>/dev/null; virsh domifaddr $d --source arp 2>/dev/null) | awk '/ipv4/{print $4}' | head -1 | cut -d/ -f1); echo "$d|$st|$mac|$mem|$unit|$cpu|$boot|$disk|$ip|$data"; done`)
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
