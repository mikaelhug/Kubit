package libvirt

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
)

const poolsDir = "/var/lib/kubit/pools"

const storageScript = `echo "@lsblk"; lsblk -J -b -o NAME,PATH,SIZE,TYPE,ROTA,TRAN,MODEL,SERIAL,RM,RO,MOUNTPOINTS,FSTYPE,LABEL,UUID 2>/dev/null; ` +
	`echo "@byid"; for l in /dev/disk/by-id/*; do [ -L "$l" ] && echo "${l##*/} $(readlink -f "$l")"; done; ` +
	`echo "@wwid"; for b in /sys/block/*; do w=$(cat "$b/wwid" "$b/device/wwid" 2>/dev/null | head -n 1); [ -n "$w" ] && echo "/dev/${b##*/} $w"; done; ` +
	`echo "@fstab"; grep -s ' ` + poolsDir + `/' /etc/fstab; ` +
	`echo "@df"; set --; for p in ` + poolsDir + `/*; do [ -d "$p" ] && set -- "$@" "$p"; done; df -B1 --output=file,target,size,avail ` + vmDir + ` "$@" 2>/dev/null; true`

const wipeFn = `wipe() { d=$(readlink -f "$1"); [ -b "$d" ] || { echo "$1: no such disk" >&2; return 1; }; ` +
	`if lsblk -nro MOUNTPOINTS "$d" | grep -q .; then echo "$1 is mounted" >&2; return 1; fi; ` +
	`if lsblk -nro TYPE "$d" | grep -qvx -e disk -e part; then echo "$1 is in use" >&2; return 1; fi; ` +
	`for p in $(lsblk -lnpo NAME "$d" | tail -n +2); do wipefs -a -q "$p" 2>/dev/null || true; done; ` +
	`wipefs -a -q "$d" && dd if=/dev/zero of="$d" bs=1M count=32 oflag=direct conv=fsync status=none; blockdev --rereadpt "$d" 2>/dev/null; true; }`

type flexBool bool

func (b *flexBool) UnmarshalJSON(raw []byte) error {
	s := strings.Trim(string(raw), `"`)
	*b = flexBool(s == "true" || s == "1")
	return nil
}

type flexInt int64

func (n *flexInt) UnmarshalJSON(raw []byte) error {
	v, _ := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64)
	*n = flexInt(v)
	return nil
}

type blockDev struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Size        flexInt    `json:"size"`
	Type        string     `json:"type"`
	Rota        flexBool   `json:"rota"`
	Tran        string     `json:"tran"`
	Model       string     `json:"model"`
	Serial      string     `json:"serial"`
	RM          flexBool   `json:"rm"`
	RO          flexBool   `json:"ro"`
	Mountpoints []string   `json:"mountpoints"`
	FSType      string     `json:"fstype"`
	Label       string     `json:"label"`
	UUID        string     `json:"uuid"`
	Children    []blockDev `json:"children"`
}

func (d blockDev) walk(fn func(blockDev)) {
	fn(d)
	for _, c := range d.Children {
		c.walk(fn)
	}
}

func sections(out string) map[string]string {
	parts := map[string]string{}
	name := ""
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "@") && !strings.ContainsAny(line, " {") {
			parts[name] = b.String()
			name = line[1:]
			b.Reset()
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	parts[name] = b.String()
	return parts
}

func parseStorage(out string) ([]labhost.HostDisk, []labhost.Pool) {
	sec := sections(out)
	var tree struct {
		Devices []blockDev `json:"blockdevices"`
	}
	_ = json.Unmarshal([]byte(sec["lsblk"]), &tree)
	links := map[string][]string{}
	for _, line := range strings.Split(sec["byid"], "\n") {
		if l, target, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			links[target] = append(links[target], l)
		}
	}
	wwids := map[string]string{}
	for _, line := range strings.Split(sec["wwid"], "\n") {
		if dev, w, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			wwids[dev] = strings.TrimSpace(w)
		}
	}
	fstab := map[string]string{}
	for _, line := range strings.Split(sec["fstab"], "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.HasPrefix(f[0], "UUID=") && path.Dir(f[1]) == poolsDir {
			fstab[strings.TrimPrefix(f[0], "UUID=")] = path.Base(f[1])
		}
	}
	type dfRow struct {
		target      string
		size, avail int64
	}
	df := map[string]dfRow{}
	for _, line := range strings.Split(sec["df"], "\n") {
		f := strings.Fields(line)
		if len(f) != 4 || !strings.HasPrefix(f[0], "/") {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		avail, _ := strconv.ParseInt(f[3], 10, 64)
		df[f[0]] = dfRow{f[1], size, avail}
	}
	var disks []labhost.HostDisk
	poolDisk := map[string]string{}
	osKey := ""
	for _, d := range tree.Devices {
		if d.Type != "disk" || bool(d.RM) || bool(d.RO) || d.Tran == "usb" || d.Size <= 0 || strings.HasPrefix(d.Name, "zram") || strings.HasPrefix(d.Name, "nbd") {
			continue
		}
		dev := d.Path
		if dev == "" {
			dev = "/dev/" + d.Name
		}
		ls := links[dev]
		slices.Sort(ls)
		hd := labhost.HostDisk{
			DiskRef:   labhost.DiskRef{DevPath: dev, Links: ls, WWID: wwids[dev], Serial: strings.TrimSpace(d.Serial), SizeBytes: uint64(d.Size), Model: strings.TrimSpace(d.Model)},
			Transport: d.Tran, Rotational: bool(d.Rota), Use: labhost.DiskFree,
		}
		hd.Key = labhost.DiskKey(hd.Links, hd.WWID, hd.Serial, dev)
		hd.ID = dev
		if l := labhost.PreferredLink(hd.Links); l != "" {
			hd.ID = "/dev/disk/by-id/" + l
		}
		var mounts []string
		var stacked, used bool
		d.walk(func(b blockDev) {
			for _, m := range b.Mountpoints {
				if m != "" {
					mounts = append(mounts, m)
				}
			}
			if b.Type != "disk" && b.Type != "part" {
				stacked = true
			}
			if name, ok := fstab[b.UUID]; ok && b.UUID != "" {
				hd.Pool = name
			}
			if b.FSType != "" && hd.Signature == "" {
				hd.Signature = b.FSType
			}
			if b.Type == "part" {
				used = true
			}
		})
		if hd.Signature == "" && used {
			hd.Signature = "partitioned"
		}
		switch {
		case slices.ContainsFunc(mounts, func(m string) bool { return m == "/" || strings.HasPrefix(m, "/boot") || m == "[SWAP]" }):
			hd.Use, hd.Pool = labhost.DiskOS, ""
			osKey = hd.Key
		case slices.ContainsFunc(mounts, func(m string) bool { return path.Dir(m) == poolsDir }):
			i := slices.IndexFunc(mounts, func(m string) bool { return path.Dir(m) == poolsDir })
			hd.Use, hd.Pool = labhost.DiskPool, path.Base(mounts[i])
		case len(mounts) > 0, stacked:
			hd.Use, hd.Pool = labhost.DiskBusy, ""
		case hd.Pool != "":
			hd.Use = labhost.DiskPool
		}
		if hd.Use == labhost.DiskPool {
			poolDisk[hd.Pool] = hd.Key
		}
		disks = append(disks, hd)
	}
	sys := labhost.Pool{Name: labhost.SystemPool, Dir: vmDir, Disk: osKey, Mounted: true}
	if r, ok := df[vmDir]; ok {
		sys.SizeBytes, sys.FreeBytes = r.size, r.avail
	}
	pools := []labhost.Pool{sys}
	names := map[string]bool{}
	for file := range df {
		if path.Dir(file) == poolsDir {
			names[path.Base(file)] = true
		}
	}
	for _, name := range fstab {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		dir := poolsDir + "/" + name
		p := labhost.Pool{Name: name, Dir: dir, Disk: poolDisk[name]}
		if r, ok := df[dir]; ok && r.target == dir {
			p.Mounted, p.SizeBytes, p.FreeBytes = true, r.size, r.avail
		}
		pools = append(pools, p)
	}
	return disks, pools
}

func createPoolScript(name, device string) string {
	return strings.NewReplacer("NAME", name, "DEVICE", shellQuote(device)).Replace(`set -e
` + wipeFn + `
dev=$(readlink -f DEVICE); dir=` + poolsDir + `/NAME
if grep -qs " $dir " /proc/mounts; then echo "$dir is already mounted" >&2; exit 1; fi
wipe DEVICE
mkfs.ext4 -F -q -m 0 -T largefile -L kubit-NAME "$dev"
uuid=$(blkid -s UUID -o value "$dev")
[ -n "$uuid" ] || { echo "no filesystem UUID on $dev" >&2; exit 1; }
mkdir -p "$dir"; chattr +i "$dir" 2>/dev/null || true
sed -i "\# $dir #d" /etc/fstab
echo "UUID=$uuid $dir ext4 noatime,nofail,x-systemd.device-timeout=30s,x-systemd.before=libvirtd.service 0 2" >> /etc/fstab
systemctl daemon-reload
mount "$dir"
chown kubit:kubit "$dir"`)
}

func removePoolScript(name string) string {
	return strings.ReplaceAll(`set -e
dir=`+poolsDir+`/NAME
if [ -n "$(find "$dir" -maxdepth 1 -name '*.qcow2' 2>/dev/null | head -n 1)" ]; then echo "NAME still holds VM images" >&2; exit 1; fi
dev=$(awk -v d="$dir" '$2==d{print $1}' /proc/mounts)
[ -z "$dev" ] || umount "$dir"
sed -i "\# $dir #d" /etc/fstab
systemctl daemon-reload
chattr -i "$dir" 2>/dev/null || true
rmdir "$dir" 2>/dev/null || true
[ -z "$dev" ] || wipefs -a -q "$dev"`, "NAME", name)
}

func (c *Client) CreatePool(ctx context.Context, name, device string) error {
	if name == labhost.SystemPool || !labhost.ValidPoolName(name) || !labhost.SafeDevice(device) {
		return fmt.Errorf("bad storage disk %s on %s", name, device)
	}
	defer labhost.ForgetCapacity(c.capacityKey())
	_, err := c.Run(ctx, createPoolScript(name, device))
	return err
}

func (c *Client) RemovePool(ctx context.Context, name string) error {
	if name == labhost.SystemPool || !labhost.ValidPoolName(name) {
		return fmt.Errorf("bad storage disk %s", name)
	}
	defer labhost.ForgetCapacity(c.capacityKey())
	_, err := c.Run(ctx, removePoolScript(name))
	return err
}

func (c *Client) wipe(ctx context.Context, device string) error {
	if !labhost.SafeDevice(device) {
		return fmt.Errorf("bad disk %s", device)
	}
	_, err := c.Run(ctx, wipeFn+"; wipe "+shellQuote(device))
	return err
}

func poolDir(pool string) string {
	if pool == "" || pool == labhost.SystemPool {
		return vmDir
	}
	return poolsDir + "/" + pool
}

func imagePath(pool, name, suffix string) string {
	return poolDir(pool) + "/" + name + suffix + ".qcow2"
}

func poolOfImage(file string) string {
	dir := path.Dir(file)
	if path.Dir(dir) == poolsDir {
		return path.Base(dir)
	}
	return labhost.SystemPool
}
