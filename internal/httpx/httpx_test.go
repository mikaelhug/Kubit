package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestGetReadsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	resp, err := Get(t.Context(), srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Errorf("body %q", b)
	}
}

func TestFetchChecksTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("payload"))
		case "/empty":
		case "/big":
			_, _ = w.Write(make([]byte, 2<<20))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var b strings.Builder
	if n, err := Fetch(t.Context(), Client, srv.URL+"/ok", &b, 0); err != nil || n != 7 || b.String() != "payload" {
		t.Errorf("ok: %d %q %v", n, b.String(), err)
	}
	for path, want := range map[string]string{"/missing": "404", "/empty": "empty", "/big": "larger than 1 MiB"} {
		if _, err := Fetch(t.Context(), Client, srv.URL+path, io.Discard, 1<<20); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", path, err, want)
		}
	}
	dest := filepath.Join(t.TempDir(), "file.bin")
	if err := FetchFile(t.Context(), Client, srv.URL+"/ok", dest, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "payload" {
		t.Errorf("file %q", got)
	}
	if err := FetchFile(t.Context(), Client, srv.URL+"/missing", dest+".x", 0o600); err == nil {
		t.Error("a failed download must fail")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), "*")); len(left) != 1 {
		t.Errorf("a failed download leaves nothing behind: %v", left)
	}
}
