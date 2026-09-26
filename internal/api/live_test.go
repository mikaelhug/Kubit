package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mikael/kubit/internal/store"
)

func TestHubReplayAndResync(t *testing.T) {
	h := newHub()
	for i := 0; i < 10; i++ {
		h.publish(Message{Kind: "status"})
	}
	missed, head, ok := h.since(7)
	if !ok || head != 10 || len(missed) != 3 || missed[0].Seq != 8 {
		t.Fatalf("replay from 7: ok=%v head=%d n=%d", ok, head, len(missed))
	}
	if missed, _, ok := h.since(10); !ok || len(missed) != 0 {
		t.Fatalf("up to date should replay nothing")
	}
	if missed, head, ok := h.since(0); !ok || head != 10 || len(missed) != 0 {
		t.Fatalf("a fresh page load replays nothing: ok=%v head=%d n=%d", ok, head, len(missed))
	}
	for i := 0; i < ringSize+5; i++ {
		h.publish(Message{Kind: "status"})
	}
	if _, _, ok := h.since(3); ok {
		t.Fatal("a seq older than the ring must demand a resync")
	}
	missed, head, ok = h.since(head + ringSize)
	if !ok || len(missed) != 5 || head != 10+ringSize+5 {
		t.Fatalf("tail replay: ok=%v n=%d head=%d", ok, len(missed), head)
	}
}

func TestLiveAcceptsOnlySameOrigin(t *testing.T) {
	s, _, _ := localServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	if _, resp, err := websocket.Dial(t.Context(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin upgrade must be refused: %v", err)
	}
	conn, _, err := websocket.Dial(t.Context(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {srv.URL}}})
	if err != nil {
		t.Fatalf("same origin: %v", err)
	}
	conn.CloseNow()
	conn, _, err = websocket.Dial(t.Context(), u, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://localhost:5173"}}})
	if err != nil {
		t.Fatalf("dev server origin: %v", err)
	}
	conn.CloseNow()
}

func TestLiveEndsWhenTheClientLeaves(t *testing.T) {
	s, _, _ := localServer(t)
	ended := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeHTTP(w, r)
		if r.URL.Path == "/api/v1/ws" {
			close(ended)
		}
	}))
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.Dial(t.Context(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn.CloseNow()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler outlived its client")
	}
	s.hub.mu.Lock()
	n := len(s.hub.subs)
	s.hub.mu.Unlock()
	if n != 0 {
		t.Errorf("subscribers left: %d", n)
	}
}

func drainScopes(ch chan Message) []string {
	var out []string
	for {
		select {
		case m := <-ch:
			if m.Kind == "refresh" {
				out = append(out, m.Cluster+"/"+m.Scope)
			}
		default:
			return out
		}
	}
}

func TestOffsiteRefreshFollowsTheStore(t *testing.T) {
	s, st, _ := localServer(t)
	ch, cancel := s.hub.subscribe()
	defer cancel()
	ctx := t.Context()
	for _, c := range []struct {
		change store.Change
		want   bool
	}{
		{store.Change{Table: "settings", Key: "kubit", Op: "put"}, true},
		{store.Change{Table: "settings", Key: "offsite.lastBackup", Op: "put"}, true},
		{store.Change{Table: "settings", Key: "ssh.pub", Op: "put"}, false},
		{store.Change{Table: "snapshots", Cluster: "c", Key: "7", Op: "delete"}, true},
	} {
		s.onChange(ctx, c.change)
		if got := slices.Contains(drainScopes(ch), "/offsite"); got != c.want {
			t.Errorf("%+v: offsite refresh %v, want %v", c.change, got, c.want)
		}
	}
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	drainScopes(ch)
	id, err := st.AddSnapshot(ctx, store.Snapshot{Cluster: "c", Path: "/x", Source: "manual", Status: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	key := strconv.FormatInt(id, 10)
	s.onChange(ctx, store.Change{Table: "snapshots", Key: key, Op: "put"})
	if slices.Contains(drainScopes(ch), "/offsite") {
		t.Error("a local-only snapshot must not refresh off-site status")
	}
	if err := st.SetSnapshotOffsite(ctx, id, "clusters/c/snapshots/x"); err != nil {
		t.Fatal(err)
	}
	s.onChange(ctx, store.Change{Table: "snapshots", Key: key, Op: "put"})
	if !slices.Contains(drainScopes(ch), "/offsite") {
		t.Error("an off-site copy must refresh off-site status")
	}
}
