package libvirt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/labhost"
)

const hpStorage = `@lsblk
{"blockdevices":[
 {"name":"sda","path":"/dev/sda","size":300000000000,"type":"disk","rota":true,"tran":"sas","model":"LOGICAL VOLUME  ","serial":"PDNLH0A","rm":false,"ro":false,"mountpoints":[null],"fstype":null,"label":null,"uuid":null,
  "children":[
   {"name":"sda1","path":"/dev/sda1","size":510000000,"type":"part","rota":true,"tran":null,"model":null,"serial":null,"rm":false,"ro":false,"mountpoints":["/boot"],"fstype":"ext2","label":null,"uuid":"aaa"},
   {"name":"sda5","path":"/dev/sda5","size":299000000000,"type":"part","rota":true,"tran":null,"model":null,"serial":null,"rm":false,"ro":false,"mountpoints":[null],"fstype":"LVM2_member","label":null,"uuid":"bbb",
    "children":[
     {"name":"lab--x--vg-root","path":"/dev/mapper/lab--x--vg-root","size":290000000000,"type":"lvm","rota":true,"tran":null,"model":null,"serial":null,"rm":false,"ro":false,"mountpoints":["/"],"fstype":"ext4","label":null,"uuid":"ccc"},
     {"name":"lab--x--vg-swap_1","path":"/dev/mapper/lab--x--vg-swap_1","size":8000000000,"type":"lvm","rota":true,"tran":null,"model":null,"serial":null,"rm":false,"ro":false,"mountpoints":["[SWAP]"],"fstype":"swap","label":null,"uuid":"ddd"}]}]},
 {"name":"sdb","path":"/dev/sdb","size":"2000000000000","type":"disk","rota":"1","tran":"sas","model":"LOGICAL VOLUME","serial":"PDNLH0B","rm":"0","ro":"0","mountpoints":[null],"fstype":"ext4","label":"old","uuid":"eee"},
 {"name":"sdc","path":"/dev/sdc","size":1000000000000,"type":"disk","rota":false,"tran":"sata","model":"Samsung SSD 870","serial":"S3Z","rm":false,"ro":false,"mountpoints":["/var/lib/kubit/pools/pool1"],"fstype":"ext4","label":"kubit-pool1","uuid":"fff"},
 {"name":"sdd","path":"/dev/sdd","size":500000000000,"type":"disk","rota":true,"tran":"sata","model":"WD","serial":"WD1","rm":false,"ro":false,"mountpoints":[null],"fstype":"ext4","label":"kubit-pool2","uuid":"ggg"},
 {"name":"sdf","path":"/dev/sdf","size":500000000000,"type":"disk","rota":true,"tran":"sata","model":"WD","serial":"WD2","rm":false,"ro":false,"mountpoints":[null],"fstype":"LVM2_member","label":null,"uuid":"hhh",
  "children":[{"name":"old--vg-data","path":"/dev/mapper/old--vg-data","size":400000000000,"type":"lvm","rota":true,"tran":null,"model":null,"serial":null,"rm":false,"ro":false,"mountpoints":[null],"fstype":"xfs","label":null,"uuid":"iii"}]},
 {"name":"sde","path":"/dev/sde","size":8000000000,"type":"disk","rota":false,"tran":"usb","model":"SD Card","serial":"","rm":true,"ro":false,"mountpoints":[null],"fstype":"vfat","label":null,"uuid":"jjj"},
 {"name":"sr0","path":"/dev/sr0","size":1073741312,"type":"rom","rota":false,"tran":"sata","model":"DVD","serial":"","rm":true,"ro":false,"mountpoints":[null],"fstype":null,"label":null,"uuid":null}
]}
@byid
scsi-3600508b1001ca /dev/sda
wwn-0x600508b1001ca /dev/sda
scsi-3600508b1001ca-part1 /dev/sda1
wwn-0x600508b1001cb /dev/sdb
ata-Samsung_SSD_870_S3Z /dev/sdc
usb-SD_Card-0:0 /dev/sde
@wwid
/dev/sda naa.600508b1001ca
/dev/sdb naa.600508b1001cb
@fstab
UUID=fff /var/lib/kubit/pools/pool1 ext4 noatime,nofail 0 2
UUID=ggg /var/lib/kubit/pools/pool2 ext4 noatime,nofail 0 2
@df
File                       Mounted on                 1B-blocks     Avail
/var/lib/kubit/vms         /                          290000000000  200000000000
/var/lib/kubit/pools/pool1 /var/lib/kubit/pools/pool1 1000000000000 900000000000
/var/lib/kubit/pools/pool2 /                          290000000000  200000000000
`

func TestParseStorageOnAnHPServer(t *testing.T) {
	disks, pools := parseStorage(hpStorage)
	byDev := map[string]labhost.HostDisk{}
	for _, d := range disks {
		byDev[d.DevPath] = d
	}
	if len(disks) != 5 {
		t.Fatalf("USB and optical drives are no candidates: %+v", disks)
	}
	want := map[string]struct{ use, pool, key, id string }{
		"/dev/sda": {labhost.DiskOS, "", "wwn-0x600508b1001ca", "/dev/disk/by-id/wwn-0x600508b1001ca"},
		"/dev/sdb": {labhost.DiskFree, "", "wwn-0x600508b1001cb", "/dev/disk/by-id/wwn-0x600508b1001cb"},
		"/dev/sdc": {labhost.DiskPool, "pool1", "ata-Samsung_SSD_870_S3Z", "/dev/disk/by-id/ata-Samsung_SSD_870_S3Z"},
		"/dev/sdd": {labhost.DiskPool, "pool2", "serial-WD1", "/dev/sdd"},
		"/dev/sdf": {labhost.DiskBusy, "", "serial-WD2", "/dev/sdf"},
	}
	for dev, w := range want {
		d := byDev[dev]
		if d.Use != w.use || d.Pool != w.pool || d.Key != w.key || d.ID != w.id {
			t.Errorf("%s: %+v, want %+v", dev, d, w)
		}
	}
	if byDev["/dev/sdb"].Signature != "ext4" || byDev["/dev/sdb"].SizeBytes != 2000000000000 || !byDev["/dev/sdb"].Rotational || byDev["/dev/sda"].WWID != "naa.600508b1001ca" || byDev["/dev/sda"].Model != "LOGICAL VOLUME" {
		t.Errorf("disk details: %+v %+v", byDev["/dev/sdb"], byDev["/dev/sda"])
	}
	if len(pools) != 3 {
		t.Fatalf("pools: %+v", pools)
	}
	sys, p1, p2 := pools[0], pools[1], pools[2]
	if sys.Name != labhost.SystemPool || sys.Disk != "wwn-0x600508b1001ca" || sys.FreeBytes != 200000000000 || !sys.Mounted {
		t.Errorf("system pool: %+v", sys)
	}
	if p1.Name != "pool1" || !p1.Mounted || p1.FreeBytes != 900000000000 || p1.Disk != "ata-Samsung_SSD_870_S3Z" {
		t.Errorf("pool1: %+v", p1)
	}
	if p2.Name != "pool2" || p2.Mounted || p2.FreeBytes != 0 || p2.Disk != "serial-WD1" {
		t.Errorf("an unmounted pool reports no space of the root filesystem: %+v", p2)
	}
}

func stubs(t *testing.T, dir string, bins map[string]string) []string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range bins {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "LOG="+filepath.Join(dir, "log"))
}

var storageBins = map[string]string{
	"readlink":   `echo "$2"`,
	"lsblk":      `case "$2" in MOUNTPOINTS) [ -n "$MOUNTED" ] && echo /mnt; echo;; TYPE) echo disk; [ -n "$LVM" ] && echo lvm;; NAME) echo "$3";; esac; true`,
	"wipefs":     `echo "wipefs $*" >> "$LOG"`,
	"dd":         `echo "dd $*" >> "$LOG"`,
	"blockdev":   `echo "blockdev $*" >> "$LOG"`,
	"mkfs.ext4":  `echo "mkfs.ext4 $*" >> "$LOG"`,
	"blkid":      `echo uuid-1`,
	"chattr":     `echo "chattr $*" >> "$LOG"`,
	"systemctl":  `true`,
	"mount":      `echo "mount $*" >> "$LOG"`,
	"umount":     `echo "umount $*" >> "$LOG"`,
	"chown":      `echo "chown $*" >> "$LOG"`,
	"mountpoint": `true`,
	"sed":        `if [ "$1" = -i ]; then /usr/bin/sed "$2" "$3" > "$3.tmp" && mv "$3.tmp" "$3"; else exec /usr/bin/sed "$@"; fi`,
}

func storageScriptIn(dir, script string) string {
	return strings.NewReplacer(poolsDir, filepath.Join(dir, "pools"), "/etc/fstab", filepath.Join(dir, "fstab"), "/proc/mounts", filepath.Join(dir, "mounts"), `[ -b "$d" ]`, "true").Replace(script)
}

func TestCreatePoolFormatsMountsAndRefusesDisksInUse(t *testing.T) {
	for _, script := range []string{createPoolScript("pool1", "/dev/disk/by-id/wwn-0xb"), removePoolScript("pool1"), "set -e; " + wipeFn + "; wipe /dev/sdb"} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("syntax: %v %s\n%s", err, out, script)
		}
	}
	run := func(env ...string) (string, string, error) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "fstab"), []byte("UUID=root / ext4 defaults 0 1\nUUID=stale "+filepath.Join(dir, "pools", "pool1")+" ext4 noatime 0 2\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "mounts"), nil, 0o644)
		cmd := exec.Command("sh", "-c", storageScriptIn(dir, createPoolScript("pool1", "/dev/disk/by-id/wwn-0xb")))
		cmd.Env = append(stubs(t, dir, storageBins), env...)
		out, err := cmd.CombinedOutput()
		log, _ := os.ReadFile(filepath.Join(dir, "log"))
		fstab, _ := os.ReadFile(filepath.Join(dir, "fstab"))
		return string(out) + string(log), string(fstab), err
	}
	log, fstab, err := run()
	if err != nil {
		t.Fatalf("create: %v\n%s", err, log)
	}
	wipe, mkfs := strings.Index(log, "wipefs -a -q /dev/disk/by-id/wwn-0xb"), strings.Index(log, "mkfs.ext4 -F -q -m 0 -T largefile -L kubit-pool1 /dev/disk/by-id/wwn-0xb")
	if wipe < 0 || mkfs < wipe || !strings.Contains(log, "chattr +i") || !strings.Contains(log, "mount ") {
		t.Errorf("wipe, format, protect and mount in that order:\n%s", log)
	}
	if strings.Contains(fstab, "UUID=stale") || !strings.Contains(fstab, "UUID=uuid-1 ") || !strings.Contains(fstab, "/pools/pool1 ext4 noatime,nofail,x-systemd.device-timeout=30s,x-systemd.before=libvirtd.service 0 2") || !strings.Contains(fstab, "UUID=root / ext4") {
		t.Errorf("fstab:\n%s", fstab)
	}
	for _, c := range []struct{ env, want string }{{"MOUNTED=1", "is mounted"}, {"LVM=1", "is in use"}} {
		log, _, err := run(c.env)
		if err == nil || !strings.Contains(log, c.want) || strings.Contains(log, "mkfs.ext4") || strings.Contains(log, "wipefs") {
			t.Errorf("%s: a disk in use must be refused before anything is written: %v\n%s", c.env, err, log)
		}
	}
}

func TestRemovePoolKeepsImagesAndWipesAnEmptyDisk(t *testing.T) {
	dir := t.TempDir()
	pool := filepath.Join(dir, "pools", "pool1")
	_ = os.MkdirAll(pool, 0o755)
	_ = os.WriteFile(filepath.Join(pool, "db.qcow2"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "fstab"), []byte("UUID=uuid-1 "+pool+" ext4 noatime 0 2\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "mounts"), []byte("/dev/sdc "+pool+" ext4 rw 0 0\n"), 0o644)
	run := func() (string, error) {
		cmd := exec.Command("sh", "-c", storageScriptIn(dir, removePoolScript("pool1")))
		cmd.Env = stubs(t, dir, storageBins)
		out, err := cmd.CombinedOutput()
		log, _ := os.ReadFile(filepath.Join(dir, "log"))
		return string(out) + string(log), err
	}
	if out, err := run(); err == nil || !strings.Contains(out, "still holds VM images") || strings.Contains(out, "umount") {
		t.Fatalf("a pool with images must stay: %v\n%s", err, out)
	}
	_ = os.Remove(filepath.Join(pool, "db.qcow2"))
	out, err := run()
	if err != nil || !strings.Contains(out, "umount "+pool) || !strings.Contains(out, "wipefs -a -q /dev/sdc") {
		t.Fatalf("release: %v\n%s", err, out)
	}
	if fstab, _ := os.ReadFile(filepath.Join(dir, "fstab")); strings.Contains(string(fstab), pool) {
		t.Errorf("fstab keeps the pool: %s", fstab)
	}
}

func TestDomainXMLPlacesImagesAndWholeDisks(t *testing.T) {
	spec := labhost.VMSpec{Name: "db", MAC: "52:54:00:6b:01:02", CPUs: 2, MemMiB: 4096, DiskGiB: 40, DataGiB: 100, System: labhost.Placement{Pool: "pool1"}, Data: labhost.Placement{Pool: "pool2"}}
	xml, err := DomainXML(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<source file='/var/lib/kubit/pools/pool1/db.qcow2'/>", "<source file='/var/lib/kubit/pools/pool2/db-data.qcow2'/>", "<nvram template='/usr/share/OVMF/OVMF_VARS_4M.fd'>/var/lib/kubit/vms/db.nvram</nvram>"} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %q", want)
		}
	}
	spec.System, spec.Data = labhost.Placement{Device: "/dev/disk/by-id/wwn-0xa"}, labhost.Placement{Device: "/dev/disk/by-id/wwn-0xb"}
	xml, err = DomainXML(spec)
	if err != nil {
		t.Fatal(err)
	}
	vda := strings.Index(xml, "<source dev='/dev/disk/by-id/wwn-0xa'/>\n      <target dev='vda' bus='virtio'/>")
	vdb := strings.Index(xml, "<source dev='/dev/disk/by-id/wwn-0xb'/>\n      <target dev='vdb' bus='virtio'/>")
	if vda < 0 || vdb < vda || strings.Count(xml, "<disk type='block' device='disk'>") != 2 || !strings.Contains(xml, "type='raw' cache='none' io='native' discard='unmap'") || strings.Contains(xml, ".qcow2") {
		t.Errorf("whole disks:\n%s", xml)
	}
	if !kernelBlock.MatchString(strings.Replace(xml, "<boot dev='hd'/>", "<kernel>k</kernel>\n    <initrd>i</initrd>\n    <cmdline>c</cmdline>", 1)) {
		t.Error("the kernel block must stay replaceable")
	}
	for _, bad := range []labhost.Placement{{Device: "/dev/sda;reboot"}, {Pool: "../etc"}} {
		spec.System = bad
		if _, err := DomainXML(spec); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
}

func TestParseListReadsDisksFromTheDomain(t *testing.T) {
	out := `@@vm lab-vm-01|running|192.168.1.50
<domain type='kvm'>
  <name>lab-vm-01</name>
  <memory unit='KiB'>3145728</memory>
  <vcpu placement='static'>2</vcpu>
  <os>
    <type arch='x86_64' machine='pc-q35-10.0'>hvm</type>
    <kernel>/var/lib/kubit/boot/v1.14.0-376567988ad3/kernel-amd64</kernel>
  </os>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='/var/lib/kubit/vms/lab-vm-01.qcow2'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='bridge'>
      <mac address='52:54:00:6b:01:01'/>
      <source bridge='br0'/>
    </interface>
  </devices>
</domain>
@@size /var/lib/kubit/vms/lab-vm-01.qcow2|64424509440
@@vm db|shut off|
<domain type='kvm'>
  <name>db</name>
  <memory unit='MiB'>4096</memory>
  <vcpu>4</vcpu>
  <os><type arch='x86_64' machine='q35'>hvm</type><boot dev='hd'/></os>
  <devices>
    <disk type='file' device='disk'><source file='/var/lib/kubit/pools/pool1/db.qcow2'/><target dev='vda' bus='virtio'/></disk>
    <disk type='block' device='disk'><source dev='/dev/disk/by-id/wwn-0xb'/><target dev='vdb' bus='virtio'/></disk>
    <interface type='bridge'><mac address='52:54:00:6b:01:02'/></interface>
  </devices>
</domain>
@@size /var/lib/kubit/pools/pool1/db.qcow2|21474836480
@@size /dev/disk/by-id/wwn-0xb|2000398934016
`
	vms := parseList(out)
	if len(vms) != 2 {
		t.Fatalf("vms: %+v", vms)
	}
	a, b := vms[0], vms[1]
	if a.Name != "lab-vm-01" || a.State != "running" || a.IP != "192.168.1.50" || a.MAC != "52:54:00:6b:01:01" || a.MemMiB != 3072 || a.CPUs != 2 || a.Boot != "talos" || a.DiskGiB != 60 || len(a.Disks) != 1 || a.Disks[0].Pool != labhost.SystemPool {
		t.Errorf("legacy VM: %+v", a)
	}
	if b.State != "shut off" || b.MemMiB != 4096 || b.CPUs != 4 || b.Boot != "disk" || b.DiskGiB != 20 || b.DataGiB != 1863 || len(b.Disks) != 2 || b.Disks[0].Pool != "pool1" || b.Disks[1].Device != "/dev/disk/by-id/wwn-0xb" || b.Disks[1].Pool != "" {
		t.Errorf("placed VM: %+v", b)
	}
	for _, script := range []string{listScript, deleteScript("db"), qcow2Script("/var/lib/kubit/pools/pool1/db.qcow2", 40), storageScript} {
		if out, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("syntax: %v %s\n%s", err, out, script)
		}
	}
}

func TestDeleteWipesOnlyItsWholeDisks(t *testing.T) {
	dir := t.TempDir()
	bins := map[string]string{
		"virsh": `case "$1" in domblklist) printf 'Type Device Target Source\n------\nfile disk vda /var/lib/kubit/pools/pool1/db.qcow2\nblock disk vdb /dev/disk/by-id/wwn-0xb\nfile cdrom sda /x.iso\n';; dominfo) exit 1;; esac; true`,
		"rm":    `echo "rm $*" >> "$LOG"`,
	}
	for k, v := range storageBins {
		bins[k] = v
	}
	cmd := exec.Command("sh", "-c", storageScriptIn(dir, deleteScript("db")))
	cmd.Env = stubs(t, dir, bins)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.Contains(string(log), "wipefs -a -q /dev/disk/by-id/wwn-0xb") || strings.Contains(string(log), "x.iso") || !strings.Contains(string(log), "/pools/*/db.qcow2") {
		t.Errorf("delete:\n%s", log)
	}
}

func TestMetricsReportEveryStorageDisk(t *testing.T) {
	pools := parsePoolUsage("load1=0.1\npool=/var/lib/kubit/vms / 100 400\npool=/var/lib/kubit/pools/pool1 /var/lib/kubit/pools/pool1 900 1000\npool=/var/lib/kubit/pools/pool2 / 100 400\n")
	if len(pools) != 3 || pools[0].Name != labhost.SystemPool || pools[1].Name != "pool1" || !pools[1].Mounted || pools[2].Mounted {
		t.Fatalf("pools: %+v", pools)
	}
}
