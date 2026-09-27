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

func TestDownloadWaitsForSlowHeaders(t *testing.T) {
	if Download.Transport.(*http.Transport).ResponseHeaderTimeout != 0 {
		t.Fatal("downloads must wait for an image build before headers arrive")
	}
	if Revalidate.Transport.(*http.Transport).ResponseHeaderTimeout != 10*time.Second {
		t.Fatal("revalidating a cached copy gives up quickly")
	}
	if transport.ResponseHeaderTimeout == 0 {
		t.Fatal("API calls keep their header timeout")
	}
}

func TestFetchIfChangedSendsTheServersValidators(t *testing.T) {
	changed := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	etag := `"v1"`
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("If-None-Match")+"|"+r.Header.Get("If-Modified-Since"))
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, "x", changed, strings.NewReader("body-"+etag))
	}))
	defer srv.Close()
	var b strings.Builder
	v, err := FetchIfChanged(t.Context(), srv.Client(), srv.URL, Validators{}, &b)
	if err != nil || b.String() != `body-"v1"` || v.ETag != etag || v.LastModified != changed.Format(http.TimeFormat) {
		t.Fatalf("first fetch: %v %q %+v", err, b.String(), v)
	}
	b.Reset()
	if _, err := FetchIfChanged(t.Context(), srv.Client(), srv.URL, v, &b); err != ErrNotModified || b.Len() != 0 {
		t.Fatalf("unchanged: want ErrNotModified and nothing written, got %v %q", err, b.String())
	}
	if sent[1] != etag+"|"+changed.Format(http.TimeFormat) {
		t.Errorf("the stored validators go back verbatim, sent %q", sent[1])
	}
	etag = `"v2"`
	older := changed.Add(-24 * time.Hour)
	changed = older
	b.Reset()
	got, err := FetchIfChanged(t.Context(), srv.Client(), srv.URL, v, &b)
	if err != nil || b.String() != `body-"v2"` || got.ETag != etag || got.LastModified != older.Format(http.TimeFormat) {
		t.Errorf("a new file with an older Last-Modified must be fetched: %v %q %+v", err, b.String(), got)
	}
	if _, err := FetchIfChanged(t.Context(), srv.Client(), srv.URL+"/x", Validators{}, io.Discard); err != nil {
		t.Errorf("an unconditional fetch never reports not modified: %v", err)
	}
}
