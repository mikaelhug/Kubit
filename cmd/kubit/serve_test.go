package main

import (
	"bytes"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestServeFailsFastOnABusyPortOrHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KUBIT_HOME", home)
	t.Setenv(store.EnvMasterKey, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	serve := func() (string, error) {
		cmd := serveCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--addr", busy.Addr().String()})
		cmd.SetContext(t.Context())
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := serve()
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("a busy port must fail the start: %v", err)
	}
	if strings.Contains(out, "listening on") {
		t.Errorf("nothing listens, so nothing may say so: %q", out)
	}
	lock, err := store.LockHome(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := serve(); err == nil || !strings.Contains(err.Error(), "another kubit serve") {
		t.Errorf("a held home must fail before anything else: %v", err)
	}
}
