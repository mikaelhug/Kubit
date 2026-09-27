package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"testing"
)

func TestInvokingUserHome(t *testing.T) {
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
	for name, c := range map[string]struct {
		euid int
		env  map[string]string
	}{
		"not root":        {501, map[string]string{"SUDO_USER": "op"}},
		"explicit home":   {0, map[string]string{"SUDO_USER": "op", "KUBIT_HOME": "/srv/kubit"}},
		"root login":      {0, map[string]string{}},
		"sudo from root":  {0, map[string]string{"SUDO_USER": "root"}},
		"unknown account": {0, map[string]string{"SUDO_USER": "ghost"}},
	} {
		if got := invokingUserHome(c.euid, env(c.env), lookup); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestHomeOwner(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	if uid, gid, ok := homeOwner("/nowhere/.kubit", env(map[string]string{"SUDO_UID": "501", "SUDO_GID": "20"})); !ok || uid != 501 || gid != 20 {
		t.Errorf("sudo ids: %d %d %v", uid, gid, ok)
	}
	home := filepath.Join(t.TempDir(), ".kubit")
	uid, _, ok := homeOwner(home, env(nil))
	if os.Geteuid() != 0 && (!ok || uid != os.Geteuid()) {
		t.Errorf("a missing home is owned like its parent: %d %v", uid, ok)
	}
	if _, _, ok := homeOwner("/", env(nil)); ok {
		t.Error("a root-owned home is left to root")
	}
	if err := ensureDir(home, filepath.Join(home, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(home, "cache")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("cache dir: %v %v", fi, err)
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
