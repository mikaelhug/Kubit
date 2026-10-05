package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecideCacheIsBounded(t *testing.T) {
	c := newDecideCache(4)
	now := time.Now()
	for i := range 10 {
		c.put(fmt.Sprintf("aa:00:00:00:00:%02x", i), "talos", now, decideTTL)
		if len(c.entries) > 4 {
			t.Fatalf("cache grew to %d", len(c.entries))
		}
	}
	if _, ok := c.get("aa:00:00:00:00:09", now); !ok {
		t.Error("the newest answer must be cached")
	}
	if _, ok := c.get("aa:00:00:00:00:09", now.Add(decideTTL+time.Second)); ok {
		t.Error("answers expire")
	}
}

func TestDeciderCachesBadAnswersBriefly(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	decide := pxeDecider(srv.URL, "", log.New(io.Discard, "", 0))
	for range 5 {
		if got := decide("aa:00:00:00:00:01"); got != "" {
			t.Fatalf("undecodable answer gave %q", got)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("a bad answer was fetched %d times in a row", n)
	}
}

func TestDeciderFailsClosedOnAnErrorStatus(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusUnauthorized)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"Boot":"talos","Reason":"x"}`))
	}))
	defer srv.Close()
	decide := pxeDecider(srv.URL, "", log.New(io.Discard, "", 0))
	for _, code := range []int32{http.StatusUnauthorized, http.StatusInternalServerError} {
		status.Store(code)
		if got := decide("aa:00:00:00:00:01"); got != "" {
			t.Fatalf("status %d gave %q", code, got)
		}
	}
	status.Store(http.StatusOK)
	if got := decide("aa:00:00:00:00:01"); got != "talos" {
		t.Errorf("an error status must not be cached as a decision: %q", got)
	}
}

func TestRepoDeciderBootsMembersLocally(t *testing.T) {
	dir := t.TempDir()
	spec := "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp-01, ip: 10.0.0.10, mac: \"AA:BB:CC:DD:EE:01\", role: controlplane, installDisk: {path: /dev/sda}}\n"
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	open, closed := repoDecider([]string{dir}, false, logger), repoDecider([]string{dir}, true, logger)
	if got := open("aa:bb:cc:dd:ee:01"); got != "local" {
		t.Errorf("member: %q", got)
	}
	if got := open("aa:bb:cc:dd:ee:02"); got != "talos" {
		t.Errorf("stranger, open: %q", got)
	}
	if got := closed("aa:bb:cc:dd:ee:02"); got != "local" {
		t.Errorf("stranger, closed: %q", got)
	}
	if got := repoDecider([]string{t.TempDir()}, false, logger)("aa:bb:cc:dd:ee:02"); got != "" {
		t.Errorf("unreadable repo must not offer anything: %q", got)
	}
}
