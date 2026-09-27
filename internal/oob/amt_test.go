package oob

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

const enumerateReply = `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.xmlsoap.org/ws/2004/09/enumeration"><a:Header></a:Header><a:Body><g:EnumerateResponse><g:EnumerationContext>01000000-0000-0000-0000-000000000000</g:EnumerationContext></g:EnumerateResponse></a:Body></a:Envelope>`

type redirect struct{ to *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

func fakeAMT(t *testing.T, h http.HandlerFunc) *amt {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &amt{c: Config{Type: "amt", Host: "amt.test", User: "admin", Password: "pw"}, transport: redirect{u}}
}

func TestAMTAliveIsOneEnumerate(t *testing.T) {
	var calls atomic.Int32
	a := fakeAMT(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(enumerateReply))
	})
	if err := a.alive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("liveness took %d requests", calls.Load())
	}
	down := fakeAMT(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	if err := down.alive(context.Background()); err == nil {
		t.Error("a refused login is not alive")
	}
}

func TestAMTCallsAreBoundByTheDeadline(t *testing.T) {
	a := fakeAMT(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := a.Probe(ctx); err == nil {
		t.Fatal("a silent AMT must fail the probe")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("probe outlived its deadline: %s", d)
	}
}

func TestAMTStopsBetweenCallsWhenCancelled(t *testing.T) {
	cancelling := func() (*amt, context.Context, *atomic.Int32) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		var calls atomic.Int32
		a := fakeAMT(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			cancel()
			w.Write([]byte(enumerateReply))
		})
		return a, ctx, &calls
	}
	a, ctx, calls := cancelling()
	if err := a.Power(ctx, BootPXE); err == nil {
		t.Error("a cancelled power request must fail")
	}
	if calls.Load() != 1 {
		t.Errorf("%d requests after cancellation", calls.Load()-1)
	}
	a, ctx, calls = cancelling()
	info, err := a.Probe(ctx)
	if err != nil || info.Power != "unknown" || calls.Load() != 1 {
		t.Errorf("probe after cancellation: %+v %v, %d requests", info, err, calls.Load())
	}
}
