package httpx

import (
	"context"
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

var Download = &http.Client{Transport: downloads()}

func downloads() *http.Transport {
	t := transport.Clone()
	t.ResponseHeaderTimeout = 0
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

func Fetch(ctx context.Context, c *http.Client, url string, w io.Writer, limit int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	body := io.Reader(resp.Body)
	if limit > 0 {
		body = io.LimitReader(resp.Body, limit+1)
	}
	n, err := io.Copy(w, body)
	switch {
	case err != nil:
		return n, err
	case limit > 0 && n > limit:
		return n, fmt.Errorf("download %s: larger than %d MiB", url, limit>>20)
	case n == 0:
		return n, fmt.Errorf("download %s: empty response body", url)
	case resp.ContentLength > 0 && n != resp.ContentLength:
		return n, fmt.Errorf("download %s: incomplete (%d of %d bytes)", url, n, resp.ContentLength)
	}
	return n, nil
}

func FetchFile(ctx context.Context, c *http.Client, url, path string, perm os.FileMode) error {
	return fsx.WriteStream(path, perm, func(w io.Writer) error {
		_, err := Fetch(ctx, c, url, w, 0)
		return err
	})
}
