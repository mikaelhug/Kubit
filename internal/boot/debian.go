package boot

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/ulikunitz/xz"
)

// Loader is the systemd-boot EFI binary for an arch, taken from Debian's own
// systemd-boot-efi package so the installer CD boots exactly what the host will run.
func (c *Cache) Loader(ctx context.Context, arch string) (string, error) {
	deb := "amd64"
	inner, name := "systemd-bootx64.efi", "BOOTX64.EFI"
	if arch == "arm64" {
		deb, inner, name = "arm64", "systemd-bootaa64.efi", "BOOTAA64.EFI"
	}
	out := filepath.Join(c.Dir, "systemd-boot-"+arch+"-"+name)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	pool := labhost.DebianMirror + "/pool/main/s/systemd/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pool, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	index, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	re := regexp.MustCompile(`systemd-boot-efi_[^"]*_` + deb + `\.deb`)
	names := re.FindAllString(string(index), -1)
	if len(names) == 0 {
		return "", fmt.Errorf("systemd-boot-efi for %s not found in %s", arch, pool)
	}
	sort.Strings(names)
	debPath, err := c.Path(ctx, pool+names[len(names)-1])
	if err != nil {
		return "", err
	}
	efi, err := extractDeb(debPath, "./usr/lib/systemd/boot/efi/"+inner)
	if err != nil {
		return "", fmt.Errorf("%s: %w", names[len(names)-1], err)
	}
	if err := os.WriteFile(out+".part", efi, 0o600); err != nil {
		return "", err
	}
	return out, os.Rename(out+".part", out)
}

// extractDeb pulls one file out of a .deb (ar archive with data.tar.{xz,gz}).
func extractDeb(path, want string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "!<arch>\n" {
		return nil, fmt.Errorf("not an ar archive")
	}
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return nil, fmt.Errorf("data.tar not found")
		}
		name := strings.TrimSpace(string(hdr[0:16]))
		var size int64
		fmt.Sscanf(strings.TrimSpace(string(hdr[48:58])), "%d", &size)
		body := io.LimitReader(r, size)
		if strings.HasPrefix(name, "data.tar") {
			var tr io.Reader
			switch {
			case strings.HasSuffix(name, ".xz"):
				tr, err = xz.NewReader(body)
			case strings.HasSuffix(name, ".gz"):
				tr, err = gzip.NewReader(body)
			default:
				tr = body
			}
			if err != nil {
				return nil, err
			}
			t := tar.NewReader(tr)
			for {
				h, err := t.Next()
				if err != nil {
					return nil, fmt.Errorf("%s not in data.tar", want)
				}
				if h.Name == want {
					return io.ReadAll(t)
				}
			}
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			return nil, err
		}
		if size%2 == 1 {
			r.ReadByte()
		}
	}
}

// DebianISO builds the installer CD for one lab host: systemd-boot, the netboot
// kernel and initrd, and a single entry carrying the preseed command line.
func (c *Cache) DebianISO(ctx context.Context, arch, hostname, cmdline string) (string, error) {
	loader, err := c.Loader(ctx, arch)
	if err != nil {
		return "", err
	}
	kernel, err := c.Path(ctx, labhost.NetbootURL(arch, "linux"))
	if err != nil {
		return "", err
	}
	initrd, err := c.Path(ctx, labhost.NetbootURL(arch, "initrd.gz"))
	if err != nil {
		return "", err
	}
	efiName := "BOOTX64.EFI"
	if arch == "arm64" {
		efiName = "BOOTAA64.EFI"
	}
	out := filepath.Join(c.Dir, "media", "debian-"+hostname+".iso")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	var entry bytes.Buffer
	fmt.Fprintf(&entry, "title Debian installer (Kubit lab host %s)\nlinux /linux\ninitrd /initrd.gz\noptions %s\n", hostname, cmdline)
	return out, BuildISO(out, "KUBIT", []Entry{
		{Path: "/EFI/BOOT/" + efiName, Src: loader},
		{Path: "/linux", Src: kernel},
		{Path: "/initrd.gz", Src: initrd},
		{Path: "/loader/loader.conf", Data: []byte("default debian\ntimeout 1\neditor no\n")},
		{Path: "/loader/entries/debian.conf", Data: entry.Bytes()},
	})
}

// TalosISO is the Image Factory's bootable ISO for a schematic, cached.
func (c *Cache) TalosISO(ctx context.Context, factoryURL, schematic, version, arch string) (string, error) {
	return c.Path(ctx, fmt.Sprintf("%s/image/%s/%s/metal-%s.iso", factoryURL, schematic, version, arch))
}
