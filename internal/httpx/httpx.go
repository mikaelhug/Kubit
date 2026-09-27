package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mikael/kubit/internal/fsx"
)

var transport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          32,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: time.Second,
}

var Client = &http.Client{Transport: transport, Timeout: time.Minute}

var Download = &http.Client{Transport: headerTimeout(0)}

var Revalidate = &http.Client{Transport: headerTimeout(10 * time.Second)}

func headerTimeout(d time.Duration) *http.Transport {
	t := transport.Clone()
	t.ResponseHeaderTimeout = d
	return t
}

func Get(ctx context.Context, url string, within time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, within)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := Client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

var ErrNotModified = errors.New("not modified")

type Validators struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

func (v Validators) conditional() bool { return v.ETag != "" || v.LastModified != "" }

func Fetch(ctx context.Context, c *http.Client, url string, w io.Writer, limit int64) (int64, error) {
	n, _, err := fetch(ctx, c, url, Validators{}, w, limit)
	return n, err
}

func FetchIfChanged(ctx context.Context, c *http.Client, url string, cached Validators, w io.Writer) (Validators, error) {
	_, v, err := fetch(ctx, c, url, cached, w, 0)
	return v, err
}

func fetch(ctx context.Context, c *http.Client, url string, cached Validators, w io.Writer, limit int64) (int64, Validators, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, Validators{}, err
	}
	if cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	if cached.LastModified != "" {
		req.Header.Set("If-Modified-Since", cached.LastModified)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, Validators{}, err
	}
	defer resp.Body.Close()
	got := Validators{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified && cached.conditional() {
		return 0, got, ErrNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return 0, got, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	body := io.Reader(resp.Body)
	if limit > 0 {
		body = io.LimitReader(resp.Body, limit+1)
	}
	n, err := io.Copy(w, body)
	switch {
	case err != nil:
		return n, got, err
	case limit > 0 && n > limit:
		return n, got, fmt.Errorf("download %s: larger than %d MiB", url, limit>>20)
	case n == 0:
		return n, got, fmt.Errorf("download %s: empty response body", url)
	case resp.ContentLength > 0 && n != resp.ContentLength:
		return n, got, fmt.Errorf("download %s: incomplete (%d of %d bytes)", url, n, resp.ContentLength)
	}
	return n, got, nil
}

func FetchFile(ctx context.Context, c *http.Client, url, path string, perm os.FileMode) error {
	return fsx.WriteStream(path, perm, func(w io.Writer) error {
		_, err := Fetch(ctx, c, url, w, 0)
		return err
	})
}
