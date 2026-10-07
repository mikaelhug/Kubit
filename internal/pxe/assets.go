package pxe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/fsx"
	"github.com/mikaelhug/kubit/internal/httpx"
)

var ipxeURLs = map[string]string{
	FileBIOS:  "https://boot.ipxe.org/undionly.kpxe",
	FileX64:   "https://boot.ipxe.org/x86_64-efi/ipxe.efi",
	FileARM64: "https://boot.ipxe.org/arm64-efi/ipxe.efi",
}

const downloadBudget = 10 * time.Minute

type Cache struct {
	Dir      string
	mu       sync.Mutex
	inflight map[string]*download
}

type download struct {
	done chan struct{}
	err  error
}

func (d *download) wait(ctx context.Context) error {
	select {
	case <-d.done:
		return d.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewCache(dir string) *Cache {
	return &Cache{Dir: dir, inflight: map[string]*download{}}
}

func cacheName(url string) string {
	return strings.NewReplacer("https://", "", "http://", "", "/", "_").Replace(url)
}

func (c *Cache) local(url string) string { return filepath.Join(c.Dir, cacheName(url)) }

func (c *Cache) Path(ctx context.Context, url string) (string, error) {
	path := c.local(url)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	d := c.start(ctx, path, func(ctx context.Context) error {
		if err := os.MkdirAll(c.Dir, 0o700); err != nil {
			return err
		}
		return httpx.FetchFile(ctx, httpx.Download, url, path, 0o644)
	})
	if err := d.wait(ctx); err != nil {
		return "", err
	}
	return path, nil
}

func (c *Cache) start(ctx context.Context, key string, fetch func(context.Context) error) *download {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, busy := c.inflight[key]; busy {
		return d
	}
	d := &download{done: make(chan struct{})}
	c.inflight[key] = d
	go func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), downloadBudget)
		defer cancel()
		d.err = fetch(dctx)
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(d.done)
	}()
	return d
}

func (c *Cache) IPXEBinary(ctx context.Context, name string) (string, error) {
	url, ok := ipxeURLs[name]
	if !ok {
		return "", fmt.Errorf("%s: not a boot file", name)
	}
	return c.Path(ctx, url)
}

var factoryAsset = regexp.MustCompile(`^(.+_image_[0-9a-f]+)_(v[^_]+)_((?:kernel|initramfs)-[^_]+)$`)

func (c *Cache) Tidy(keep int, pinned string, partAge time.Duration) error {
	if err := fsx.SweepParts(c.Dir, partAge); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	versions := map[string][]string{}
	files := map[string][]string{}
	for _, e := range entries {
		m := factoryAsset.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		key := m[1] + "\x00" + m[2]
		if len(files[key]) == 0 {
			versions[m[1]] = append(versions[m[1]], m[2])
		}
		files[key] = append(files[key], e.Name())
	}
	var errs []error
	for schematic, vs := range versions {
		slices.SortFunc(vs, func(a, b string) int { return compareVersions(b, a) })
		for _, v := range vs[min(keep, len(vs)):] {
			if v == pinned {
				continue
			}
			for _, name := range files[schematic+"\x00"+v] {
				if err := os.Remove(filepath.Join(c.Dir, name)); err != nil && !os.IsNotExist(err) {
					errs = append(errs, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range 3 {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] - pb.n[i]
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	return strings.Compare(pa.pre, pb.pre)
}

type version struct {
	n   [3]int
	pre string
}

func versionParts(s string) version {
	var v version
	core, pre, _ := strings.Cut(strings.TrimPrefix(s, "v"), "-")
	v.pre = pre
	for i, p := range strings.SplitN(core, ".", 3) {
		v.n[i], _ = strconv.Atoi(p)
	}
	return v
}
