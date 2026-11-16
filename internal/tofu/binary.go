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

const Version = "1.13.9"

var checksums = map[string]string{
	"darwin_arm64": "29ba2f4c78556a8d25476e5070fe4745495ef649c4a3b827170eb7436d23feb3",
	"darwin_amd64": "ac5505dd19d896f327d0ab727d4b853413c694931a7602b31f93c3a3e9d46d00",
	"linux_amd64":  "bc44eb6b06446ba83bd4072114891987f590f66cca712737a7f1147c50c539ce",
	"linux_arm64":  "04f7c93828b859d420269ed30f923ffebf09157b5d7fb95bf8d88a4d208b7fa3",
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
