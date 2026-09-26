package pxe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ipxeURLs = map[string]string{
	FileBIOS:  "https://boot.ipxe.org/undionly.kpxe",
	FileX64:   "https://boot.ipxe.org/x86_64-efi/ipxe.efi",
	FileARM64: "https://boot.ipxe.org/arm64-efi/ipxe.efi",
}

type Cache struct {
	Dir      string
	mu       sync.Mutex
	inflight map[string]*sync.WaitGroup
}

func NewCache(dir string) *Cache { return &Cache{Dir: dir, inflight: map[string]*sync.WaitGroup{}} }

func (c *Cache) Path(ctx context.Context, url string) (string, error) {
	name := strings.NewReplacer("https://", "", "http://", "", "/", "_").Replace(url)
	path := filepath.Join(c.Dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	c.mu.Lock()
	if wg, busy := c.inflight[name]; busy {
		c.mu.Unlock()
		wg.Wait()
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("download of %s failed", url)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.inflight[name] = wg
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, name)
		c.mu.Unlock()
		wg.Done()
	}()
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", err
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if n == 0 {
		os.Remove(tmp)
		return "", fmt.Errorf("%s: empty response body", url)
	}
	return path, os.Rename(tmp, path)
}

func (c *Cache) IPXEBinary(ctx context.Context, name string) (string, error) {
	url, ok := ipxeURLs[name]
	if !ok {
		return "", fmt.Errorf("%s: not a boot file", name)
	}
	return c.Path(ctx, url)
}
