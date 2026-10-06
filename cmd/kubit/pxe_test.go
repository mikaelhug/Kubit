package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestPXENeverBootsADeclaredMemberIntoTheInstaller(t *testing.T) {
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
	if got := closed("aa:bb:cc:dd:ee:02"); got != "local" {
		t.Errorf("stranger, closed: %q", got)
	}
	if got := repoDecider([]string{t.TempDir()}, false, logger)("aa:bb:cc:dd:ee:02"); got != "" {
		t.Errorf("unreadable repo must not offer anything: %q", got)
	}
}
