package labhost

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	SystemPool = "system"
	DebianDisk = "debian"
)

const (
	DiskOS   = "os"
	DiskPool = "pool"
	DiskFree = "free"
	DiskBusy = "busy"
)

type DiskRef struct {
	Key       string   `json:"key"`
	DevPath   string   `json:"devPath,omitempty"`
	Links     []string `json:"links,omitempty"`
	WWID      string   `json:"wwid,omitempty"`
	Serial    string   `json:"serial,omitempty"`
	SizeBytes uint64   `json:"sizeBytes"`
	Model     string   `json:"model,omitempty"`
}

type HostDisk struct {
	DiskRef
	ID         string `json:"id"`
	Transport  string `json:"transport,omitempty"`
	Rotational bool   `json:"rotational,omitempty"`
	Use        string `json:"use"`
	Pool       string `json:"pool,omitempty"`
	Signature  string `json:"signature,omitempty"`
}

type Pool struct {
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Disk      string `json:"disk,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	FreeBytes int64  `json:"freeBytes"`
	Mounted   bool   `json:"mounted"`
}

type PoolUsage struct {
	Name    string `json:"name"`
	Used    int64  `json:"used"`
	Total   int64  `json:"total"`
	Mounted bool   `json:"mounted"`
}

type Placement struct {
	Pool   string `json:"pool,omitempty"`
	Device string `json:"device,omitempty"`
}

type VMDisk struct {
	Target string `json:"target"`
	Pool   string `json:"pool,omitempty"`
	Device string `json:"device,omitempty"`
	GiB    int    `json:"gib"`
}

type Storage interface {
	CreatePool(ctx context.Context, name, device string) error
	RemovePool(ctx context.Context, name string) error
}

var (
	safeID     = regexp.MustCompile(`^[A-Za-z0-9._:+@-]+$`)
	safeDev    = regexp.MustCompile(`^/dev/[A-Za-z0-9_-]+$`)
	partSuffix = regexp.MustCompile(`-part[0-9]+$`)
	poolName   = regexp.MustCompile(`^pool[0-9]{1,3}$`)
	unsafeRun  = regexp.MustCompile(`[^A-Za-z0-9._:+@-]+`)
)

func SafeID(s string) bool { return safeID.MatchString(s) }

func SafeDevice(path string) bool {
	if id, ok := strings.CutPrefix(path, "/dev/disk/by-id/"); ok {
		return SafeID(id)
	}
	return safeDev.MatchString(path)
}

func ValidPoolName(name string) bool { return name == SystemPool || poolName.MatchString(name) }

var linkRank = []string{"wwn-", "nvme-eui.", "scsi-3", "nvme-", "ata-", "scsi-"}

func PreferredLink(links []string) string {
	best, bestRank := "", len(linkRank)+1
	for _, l := range links {
		if partSuffix.MatchString(l) || strings.HasPrefix(l, "usb-") || !SafeID(l) {
			continue
		}
		rank := len(linkRank)
		for i, p := range linkRank {
			if strings.HasPrefix(l, p) {
				rank = i
				break
			}
		}
		if rank < bestRank || rank == bestRank && l < best {
			best, bestRank = l, rank
		}
	}
	return best
}

func DiskKey(links []string, wwid, serial, devPath string) string {
	if l := PreferredLink(links); l != "" {
		return l
	}
	if w := strings.Trim(unsafeRun.ReplaceAllString(strings.TrimSpace(wwid), "_"), "_"); w != "" {
		return "wwid-" + w
	}
	if s := strings.Trim(unsafeRun.ReplaceAllString(strings.TrimSpace(serial), "_"), "_"); s != "" {
		return "serial-" + s
	}
	return devPath
}

func (r DiskRef) Name() string {
	if r.DevPath != "" {
		return strings.TrimPrefix(r.DevPath, "/dev/")
	}
	return r.Key
}

func (r DiskRef) Label() string {
	if r.SizeBytes == 0 {
		return r.Name()
	}
	return fmt.Sprintf("%s (%d GiB)", r.Name(), r.SizeBytes>>30)
}

func MatchDisk(ref DiskRef, disks []HostDisk) (HostDisk, bool) {
	same := func(a, b string) bool {
		a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
		return a != "" && a == b
	}
	levels := []func(d HostDisk) bool{
		func(d HostDisk) bool { return ref.Key != "" && d.Key == ref.Key },
		func(d HostDisk) bool {
			return slices.ContainsFunc(ref.Links, func(l string) bool { return slices.Contains(d.Links, l) })
		},
		func(d HostDisk) bool { return same(d.WWID, ref.WWID) },
		func(d HostDisk) bool { return same(d.Serial, ref.Serial) },
		func(d HostDisk) bool {
			return ref.DevPath != "" && d.DevPath == ref.DevPath && d.SizeBytes == ref.SizeBytes
		},
	}
	for _, match := range levels {
		var found []HostDisk
		for _, d := range disks {
			if match(d) {
				found = append(found, d)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], true
		}
		return HostDisk{}, false
	}
	return HostDisk{}, false
}

func ParseDiskChoice(s string) (whole bool, key string, err error) {
	if s == "" {
		return false, "", nil
	}
	kind, key, ok := strings.Cut(s, ":")
	if !ok || key == "" || kind != "image" && kind != "whole" {
		return false, "", fmt.Errorf("disk choice %q: want image:<disk> or whole:<disk>", s)
	}
	return kind == "whole", key, nil
}

func NextPoolName(pools []Pool) string {
	for n := 1; ; n++ {
		name := "pool" + strconv.Itoa(n)
		if !slices.ContainsFunc(pools, func(p Pool) bool { return p.Name == name }) {
			return name
		}
	}
}
