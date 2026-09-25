package vfkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/labhost"
)

func (h *Host) EnsureTalosBoot(ctx context.Context, factoryURL, schematic, version, arch string) (labhost.Boot, error) {
	if len(schematic) < 12 {
		return labhost.Boot{}, fmt.Errorf("bad schematic %q", schematic)
	}
	dir := filepath.Join(h.Dir, bootDir, version+"-"+schematic[:12])
	iso := filepath.Join(dir, "metal-"+arch+".iso")
	if fi, err := os.Stat(iso); err == nil && fi.Size() > 0 {
		return labhost.Boot{ISO: iso}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return labhost.Boot{}, err
	}
	url := fmt.Sprintf("%s/image/%s/%s/metal-%s.iso", factoryURL, schematic, version, arch)
	if err := download(ctx, h.HTTP, url, iso); err != nil {
		return labhost.Boot{}, err
	}
	return labhost.Boot{ISO: iso}, nil
}

func download(ctx context.Context, c *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s: %s", url, resp.Status)
	}
	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n == 0:
		err = fmt.Errorf("empty after download: %s", url)
	case resp.ContentLength > 0 && n != resp.ContentLength:
		err = fmt.Errorf("incomplete download: %s (%d of %d bytes)", url, n, resp.ContentLength)
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, path)
}
