package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"testing"
)

func TestSudoRunUsesTheInvokingUsersHome(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	lookup := func(name string) (*user.User, error) {
		if name == "op" {
			return &user.User{Username: "op", HomeDir: "/home/op"}, nil
		}
		return nil, errors.New("unknown user")
	}
	if got := invokingUserHome(0, env(map[string]string{"SUDO_USER": "op"}), lookup); got != "/home/op/.kubit" {
		t.Errorf("sudo: %q", got)
	}
}

func TestEnsureDirChownsOnlyWhatItCreates(t *testing.T) {
	t.Setenv("SUDO_UID", "4242")
	t.Setenv("SUDO_GID", "20")
	var chowned []string
	record := func(p string, uid, gid int) error {
		if uid != 4242 || gid != 20 {
			t.Errorf("chown %s to %d:%d", p, uid, gid)
		}
		chowned = append(chowned, p)
		return nil
	}
	root := t.TempDir()
	home := filepath.Join(root, ".kubit")
	cache := filepath.Join(home, "cache", "assets")
	if err := ensureDirAs(home, cache, 0o700, true, record); err != nil {
		t.Fatal(err)
	}
	if want := []string{home, filepath.Join(home, "cache"), cache}; !slices.Equal(chowned, want) {
		t.Errorf("new dirs chowned %v, want %v", chowned, want)
	}

	chowned = nil
	if err := ensureDirAs(home, filepath.Join(home, "cache"), 0o700, true, record); err != nil {
		t.Fatal(err)
	}
	if err := ensureDirAs(home, home, 0o700, true, record); err != nil {
		t.Fatal(err)
	}
	if len(chowned) != 0 {
		t.Errorf("existing dirs must never be chowned: %v", chowned)
	}

	system := filepath.Join(root, "etc")
	if err := os.Mkdir(system, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(system, filepath.Join(home, "log")); err != nil {
		t.Fatal(err)
	}
	if err := ensureDirAs(home, filepath.Join(home, "log", "x"), 0o700, true, record); err == nil {
		t.Error("a symlink under the home must be refused as root")
	}
	if _, err := os.Stat(filepath.Join(system, "x")); !os.IsNotExist(err) || len(chowned) != 0 {
		t.Errorf("nothing is created or chowned through the symlink: %v %v", err, chowned)
	}
	if err := ensureDirAs(home, filepath.Join(home, "log"), 0o700, false, record); err != nil {
		t.Errorf("a user's own run follows the symlink: %v", err)
	}
}
