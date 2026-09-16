package boot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Cache fetches files once into dir and serves them from there afterwards, so a
// fleet of machines booting at once hits the Image Factory a single time.
type Cache struct {
	Dir      string
	mu       sync.Mutex
	inflight map[string]*sync.WaitGroup
}

func NewCache(dir string) *Cache { return &Cache{Dir: dir, inflight: map[string]*sync.WaitGroup{}} }

// Path returns the local file for url, downloading it if needed.
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	f.Close()
	return path, os.Rename(tmp, path)
}
