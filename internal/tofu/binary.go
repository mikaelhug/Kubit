// Package tofu runs OpenTofu for the in-cluster platform layer: a pinned binary, the
// rendered infra/platform root, and a plan/apply runner that streams JSON events.
package tofu

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Version is the OpenTofu release Kubit drives. Checksums are from the release's
// SHA256SUMS; bump them together.
const Version = "1.12.6"

var checksums = map[string]string{
	"darwin_arm64": "e083ee43790ab9e19ad66d9933e24a7244a1412e1d5728f37999ae2163fdac95",
	"darwin_amd64": "166388e5feed47e107e11721b6366bf91d21e47eccbced75f3cbe0c7184ffd9b",
	"linux_amd64":  "5dc43da4f750f33873dc25e94587128709e819e544b7be9016b255316153c3a8",
	"linux_arm64":  "e573979ba68a17fe7b881752051a694a7efcd970e39521f6a25775197861ed4d",
}

// Binary returns the path of a tofu binary of the pinned version, downloading it into
// binDir on first use. A PATH tofu of the same major.minor is used if present.
func Binary(ctx context.Context, binDir string) (string, error) {
	if p, err := exec.LookPath("tofu"); err == nil && sameMinor(ctx, p) {
		return p, nil
	}
	path := filepath.Join(binDir, "tofu-"+Version)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	want, ok := checksums[platform]
	if !ok {
		return "", fmt.Errorf("no pinned OpenTofu build for %s; install tofu %s on PATH", platform, Version)
	}
	url := fmt.Sprintf("https://github.com/opentofu/opentofu/releases/download/v%s/tofu_%s_%s.zip", Version, Version, platform)
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
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	archive, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
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
		tmp := path + ".part"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return "", err
		}
		out.Close()
		if err := os.Rename(tmp, path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("%s: no tofu binary in archive", url)
}

func sameMinor(ctx context.Context, path string) bool {
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return false
	}
	// First line: "OpenTofu v1.12.6"
	line, _, _ := strings.Cut(string(out), "\n")
	v := strings.TrimPrefix(strings.TrimPrefix(line, "OpenTofu "), "v")
	return minor(v) == minor(Version)
}

func minor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
