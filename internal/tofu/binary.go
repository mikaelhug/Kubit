package tofu

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/mikaelhug/kubit/internal/fsx"
	"github.com/mikaelhug/kubit/internal/httpx"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

const Version = "1.12.6"

var checksums = map[string]string{
	"darwin_arm64": "e083ee43790ab9e19ad66d9933e24a7244a1412e1d5728f37999ae2163fdac95",
	"darwin_amd64": "166388e5feed47e107e11721b6366bf91d21e47eccbced75f3cbe0c7184ffd9b",
	"linux_amd64":  "5dc43da4f750f33873dc25e94587128709e819e544b7be9016b255316153c3a8",
	"linux_arm64":  "e573979ba68a17fe7b881752051a694a7efcd970e39521f6a25775197861ed4d",
}

var resolved sync.Map

func Binary(ctx context.Context, binDir string) (string, error) {
	key := binDir + "\x00" + Version
	if p, ok := resolved.Load(key); ok {
		if _, err := os.Stat(p.(string)); err == nil {
			return p.(string), nil
		}
		resolved.Delete(key)
	}
	p, err := binary(ctx, binDir)
	if err == nil {
		resolved.Store(key, p)
	}
	return p, err
}

func binary(ctx context.Context, binDir string) (string, error) {
	if p, err := exec.LookPath("tofu"); err == nil && sameMinor(ctx, p) {
		return p, nil
	}
	path := filepath.Join(binDir, "tofu-"+Version)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	downloadMu.Lock()
	defer downloadMu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	want, ok := checksums[platform]
	if !ok {
		return "", fmt.Errorf("no pinned OpenTofu build for %s; install tofu %s on PATH", platform, Version)
	}
	url := fmt.Sprintf("https://github.com/opentofu/opentofu/releases/download/v%s/tofu_%s_%s.zip", Version, Version, platform)
	var buf bytes.Buffer
	if _, err := httpx.Fetch(ctx, httpx.Download, url, &buf, maxArchive); err != nil {
		return "", err
	}
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("%s: checksum %s does not match pinned %s", url, got, want)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "tofu" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		if err := os.MkdirAll(binDir, 0o700); err != nil {
			return "", err
		}
		if err := fsx.WriteStream(path, 0o700, func(w io.Writer) error {
			_, err := io.Copy(w, io.LimitReader(rc, maxBinary))
			return err
		}); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("%s: no tofu binary in archive", url)
}

const (
	maxArchive = 256 << 20
	maxBinary  = 512 << 20
)

var downloadMu sync.Mutex

func sameMinor(ctx context.Context, path string) bool {
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Env = childEnv()
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	line, _, _ := strings.Cut(string(out), "\n")
	v, err := utilversion.ParseMajorMinor(strings.TrimPrefix(line, "OpenTofu "))
	return err == nil && v.EqualTo(utilversion.MustParseMajorMinor(Version))
}
