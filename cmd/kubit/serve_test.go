package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestServeFailsFastOnABusyPort(t *testing.T) {
	t.Setenv("KUBIT_HOME", t.TempDir())
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	serve := func() (string, error) {
		cmd := serveCmd(false)
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
}
