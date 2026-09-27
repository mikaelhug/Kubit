package vfkit

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/httpx"
	"github.com/mikael/kubit/internal/labhost"
)

func (h *Host) EnsureTalosBoot(ctx context.Context, f *factory.Client, schematic, version, arch string) (labhost.Boot, error) {
	name, err := labhost.BootCacheName(version, schematic)
	if err != nil {
		return labhost.Boot{}, err
	}
	dir := filepath.Join(h.Dir, bootDir, name)
	iso := filepath.Join(dir, "metal-"+arch+".iso")
	if fi, err := os.Stat(iso); err == nil && fi.Size() > 0 {
		return labhost.Boot{ISO: iso}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return labhost.Boot{}, err
	}
	defer labhost.ForgetCapacity(h.capacityKey())
	if err := httpx.FetchFile(ctx, h.HTTP, f.ISOURL(schematic, version, arch), iso, 0o644); err != nil {
		return labhost.Boot{}, err
	}
	return labhost.Boot{ISO: iso}, nil
}
