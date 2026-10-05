package api

import (
	"context"
	"encoding/json"
	"fmt"
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
	replay := func(since int64) *subscription {
		sub := h.subscribe(since)
		h.unsubscribe(sub)
		return sub
	}
	sub := replay(7)
	if !sub.replay || sub.head != 10 || len(sub.missed) != 3 || sub.missed[0].seq != 8 {
		t.Fatalf("replay from 7: ok=%v head=%d n=%d", sub.replay, sub.head, len(sub.missed))
	}
	if m := decodeFrame(t, sub.missed[0]); m.Seq != 8 || m.Kind != "status" {
		t.Fatalf("frame carries its seq: %+v", m)
	}
	if sub := replay(10); !sub.replay || len(sub.missed) != 0 {
		t.Fatalf("up to date should replay nothing")
	}
	if sub := replay(0); !sub.replay || sub.head != 10 || len(sub.missed) != 0 {
		t.Fatalf("a fresh page load replays nothing: ok=%v head=%d n=%d", sub.replay, sub.head, len(sub.missed))
	}
	for i := 0; i < ringSize+5; i++ {
		h.publish(Message{Kind: "status"})
	}
	if sub := replay(3); sub.replay {
		t.Fatal("a seq older than the ring must demand a resync")
	}
	sub = replay(sub.head + ringSize)
	if !sub.replay || len(sub.missed) != 5 || sub.head != 10+ringSize+5 {
		t.Fatalf("tail replay: ok=%v n=%d head=%d", sub.replay, len(sub.missed), sub.head)
	}
}

func TestSubscribeNeverRepeatsAReplayedMessage(t *testing.T) {
	h := newHub()
	for i := 0; i < 10; i++ {
		h.publish(Message{Kind: "status"})
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				h.publish(Message{Kind: "status"})
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 200; i++ {
		h.mu.Lock()
		since := h.seq - 5
		h.mu.Unlock()
		sub := h.subscribe(since)
		last := since
		for _, f := range sub.missed {
			if f.seq != last+1 {
				t.Fatalf("replay gap or repeat: %d after %d", f.seq, last)
			}
			last = f.seq
		}
		if last != sub.head {
			t.Fatalf("replay ends at %d, head %d", last, sub.head)
		}
		for j := 0; j < 3; j++ {
			f, ok := <-sub.ch
			if !ok {
				break
			}
			if f.seq != last+1 {
				t.Fatalf("live message %d after %d", f.seq, last)
			}
			last = f.seq
		}
		h.unsubscribe(sub)
	}
}

func TestSlowSubscriberIsDisconnected(t *testing.T) {
	h := newHub()
	slow := h.subscribe(0)
	fast := h.subscribe(0)
	for i := 0; i < subscriberBuffer+1; i++ {
		h.publish(Message{Kind: "status"})
		if i < subscriberBuffer {
			<-fast.ch
		}
	}
	n := 0
	for range slow.ch {
		n++
	}
	if n != subscriberBuffer {
		t.Errorf("slow subscriber got %d buffered messages before its channel closed, want %d", n, subscriberBuffer)
	}
	h.mu.Lock()
	_, slowLeft := h.subs[slow.ch]
	_, fastLeft := h.subs[fast.ch]
	h.mu.Unlock()
	if slowLeft || !fastLeft {
		t.Errorf("subscribers after overflow: slow=%v fast=%v", slowLeft, fastLeft)
	}
	h.unsubscribe(slow)
	if f := <-fast.ch; f.seq != subscriberBuffer+1 {
		t.Errorf("a keeping-up subscriber misses nothing: got %d", f.seq)
	}
}

func TestLiveClosesAnOverflowedConnection(t *testing.T) {
	s, _ := localServer(t)
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
	defer conn.CloseNow()
	if _, _, err := conn.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	for {
		s.hub.mu.Lock()
		var sub chan frame
		for ch := range s.hub.subs {
			sub = ch
		}
		s.hub.mu.Unlock()
		if sub != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.hub.mu.Lock()
	for ch := range s.hub.subs {
		delete(s.hub.subs, ch)
		close(ch)
	}
	s.hub.mu.Unlock()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler kept a connection whose subscription overflowed")
	}
}

type decoded struct {
	Seq     int64  `json:"seq"`
	Kind    string `json:"kind"`
	Cluster string `json:"cluster"`
	Scope   string `json:"scope"`
	Key     string `json:"key"`
	Node    string `json:"node"`
	raw     string
}

func decodeFrame(t *testing.T, f frame) decoded {
	t.Helper()
	var m decoded
	if err := json.Unmarshal(f.data, &m); err != nil {
		t.Fatalf("frame %s: %v", f.data, err)
	}
	m.raw = string(f.data)
	return m
}

func TestLiveAcceptsOnlySameOrigin(t *testing.T) {
	s, _ := localServer(t)
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
	s, _ := localServer(t)
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

func drainScopes(t *testing.T, ch chan frame) []string {
	var out []string
	for {
		select {
		case f := <-ch:
			if m := decodeFrame(t, f); m.Kind == "refresh" {
				out = append(out, m.Cluster+"/"+m.Scope)
			}
		default:
			return out
		}
	}
}

func nextKind(t *testing.T, ch chan frame, kind string) (decoded, bool) {
	t.Helper()
	for {
		select {
		case f := <-ch:
			if m := decodeFrame(t, f); m.Kind == kind {
				return m, true
			}
		default:
			return decoded{}, false
		}
	}
}

func TestClusterMessageCarriesTheSpec(t *testing.T) {
	s, st := localServer(t)
	sub := s.hub.subscribe(0)
	defer s.hub.unsubscribe(sub)
	ch := sub.ch
	ctx := t.Context()
	spec := []byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: { name: c }\nspec:\n  talosVersion: v1.14.1\n  controlPlane: { endpoint: \"https://192.168.64.2:6443\" }\n  nodes:\n    - { hostname: cp-01, ip: 192.168.64.2, role: controlplane, arch: arm64, installDisk: { path: /dev/vda } }\n")
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: spec, State: "ready"}); err != nil {
		t.Fatal(err)
	}
	s.onChange(ctx, store.Change{Table: "clusters", Key: "c", Op: "put"})
	m, ok := nextKind(t, ch, "cluster")
	if !ok {
		t.Fatal("no cluster message")
	}
	if !strings.Contains(m.raw, `"talosVersion":"v1.14.1"`) {
		t.Fatalf("cluster message without spec: %s", m.raw)
	}
}

func TestSavingTheDeclarationRefreshesConfigStatus(t *testing.T) {
	s, st := localServer(t)
	sub := s.hub.subscribe(0)
	defer s.hub.unsubscribe(sub)
	ctx := t.Context()
	st.OnChange(func(c store.Change) { s.onChange(ctx, c) })
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(drainScopes(t, sub.ch), "c/config") {
		t.Error("a saved declaration must refresh the config status")
	}
}

func TestStoreChangesReachTheirViews(t *testing.T) {
	s, st := localServer(t)
	sub := s.hub.subscribe(0)
	defer s.hub.unsubscribe(sub)
	ch := sub.ch
	ctx := t.Context()
	st.OnChange(func(c store.Change) { s.onChange(ctx, c) })
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClusterSecrets(ctx, "c", store.ClusterSecrets{SecretsBundle: []byte("b"), Talosconfig: []byte("t")}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(drainScopes(t, ch), "c/certificates") {
		t.Error("new cluster secrets must refresh its certificates")
	}
	id, err := st.AddEvent(ctx, store.EventRow{Cluster: "c", Node: "n", Kind: "node.notready", Severity: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	for len(ch) > 0 {
		<-ch
	}
	if err := st.AckEvent(ctx, id); err != nil {
		t.Fatal(err)
	}
	m, ok := nextKind(t, ch, "healthAck")
	if !ok {
		t.Fatal("no healthAck message")
	}
	if m.Cluster != "c" || m.Key != strconv.FormatInt(id, 10) {
		t.Errorf("ack message: %+v", m)
	}
}

func TestForgettingAClusterSendsOneMachinesRefresh(t *testing.T) {
	s, st := localServer(t)
	ctx := t.Context()
	if err := st.PutCluster(ctx, store.ClusterRow{Name: "c", Spec: []byte("x"), State: "ready"}); err != nil {
		t.Fatal(err)
	}
	for i := range subscriberBuffer + 10 {
		row := store.NodeRow{MAC: fmt.Sprintf("02:00:00:00:%02x:%02x", i/256, i%256), IP: fmt.Sprintf("10.1.%d.%d", i/256, i%256+1), Cluster: "c", Source: "manual", State: "ready"}
		if err := st.UpsertNode(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	st.OnChange(func(c store.Change) { s.onChange(ctx, c) })
	sub := s.hub.subscribe(0)
	defer s.hub.unsubscribe(sub)
	if err := st.DeleteCluster(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for {
		select {
		case f, ok := <-sub.ch:
			if !ok {
				t.Fatalf("the subscriber was disconnected after %v", kinds)
			}
			m := decodeFrame(t, f)
			kinds = append(kinds, m.Kind+":"+m.Scope)
			continue
		default:
		}
		break
	}
	if slices.Contains(kinds, "machine:") || !slices.Contains(kinds, "refresh:machines") || len(kinds) > 3 {
		t.Errorf("frames: %v", kinds)
	}
}

func TestHubShutdownFlushesThenRefuses(t *testing.T) {
	h := newHub()
	sub := h.subscribe(0)
	h.publish(Message{Kind: "status"})
	h.shutdown(Message{Kind: "stopped"})
	var kinds []string
	for f := range sub.ch {
		kinds = append(kinds, decodeFrame(t, f).Kind)
	}
	if !slices.Equal(kinds, []string{"status", "stopped"}) {
		t.Fatalf("queued frames, then stopped, then a closed channel: %v", kinds)
	}
	if h.subscribe(0) != nil {
		t.Fatal("a stopping hub must refuse new listeners")
	}
	if h.idle() {
		t.Fatal("the listener has not left yet")
	}
	h.unsubscribe(sub)
	if !h.idle() {
		t.Fatal("no listener is left")
	}
}

func TestCloseTellsLiveClientsKubitStopped(t *testing.T) {
	s, _ := localServer(t)
	srv := httptest.NewServer(s)
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	conn, _, err := websocket.Dial(t.Context(), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("the connection ended before stopped: %v", err)
		}
		if strings.Contains(string(b), `"kind":"stopped"`) {
			break
		}
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("the connection outlived the stop")
	}
	if _, resp, err := websocket.Dial(t.Context(), u, nil); err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a stopping daemon must refuse new live connections: %v", err)
	}
	if !s.hub.idle() {
		t.Error("listeners left after the drain")
	}
}
