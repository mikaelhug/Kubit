package httpx

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGetBoundsTheWholeRequest(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	start := time.Now()
	if _, err := Get(t.Context(), slow.URL, 100*time.Millisecond); err == nil {
		t.Fatal("want a timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestFetchFileFailsOnAnErrorStatusAndLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := FetchFile(t.Context(), Client, srv.URL, dest, 0o600); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 must fail the download: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), "*")); len(left) != 0 {
		t.Errorf("a failed download leaves nothing behind: %v", left)
	}
}

func TestDownloadWaitsForSlowHeaders(t *testing.T) {
	if Download.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
		t.Fatal("downloads must wait for an image build before headers arrive")
	}
	if transport.ResponseHeaderTimeout == 0 {
		t.Fatal("API calls keep their header timeout")
	}
}
