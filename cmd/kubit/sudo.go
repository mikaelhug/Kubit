package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func adoptInvokingUserHome() {
	if h := invokingUserHome(os.Geteuid(), os.Getenv, user.Lookup); h != "" {
		_ = os.Setenv("KUBIT_HOME", h)
	}
}

func invokingUserHome(euid int, getenv func(string) string, lookup func(string) (*user.User, error)) string {
	name := getenv("SUDO_USER")
	if euid != 0 || getenv("KUBIT_HOME") != "" || name == "" || name == "root" {
		return ""
	}
	u, err := lookup(name)
	if err != nil || u.HomeDir == "" {
		return ""
	}
	return filepath.Join(u.HomeDir, ".kubit")
}

func ensureDir(home, dir string, perm os.FileMode) error {
	return ensureDirAs(home, dir, perm, os.Geteuid() == 0, os.Lchown)
}

func ensureDirAs(home, dir string, perm os.FileMode, root bool, chown func(string, int, int) error) error {
	created, err := mkdirChain(home, dir, perm, root)
	if err != nil || !root || len(created) == 0 {
		return err
	}
	uid, gid, ok := homeOwner(home, os.Getenv)
	if !ok {
		return nil
	}
	for _, p := range created {
		if err := chown(p, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

func mkdirChain(home, dir string, perm os.FileMode, noLinks bool) ([]string, error) {
	home, dir = filepath.Clean(home), filepath.Clean(dir)
	var chain []string
	for p := dir; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	var created []string
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		fi, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if err = os.Mkdir(p, perm); err == nil {
				created = append(created, p)
				continue
			}
			if !os.IsExist(err) {
				return created, err
			}
			fi, err = os.Lstat(p)
		}
		if err != nil {
			return created, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if noLinks && within(home, p) {
				return created, fmt.Errorf("%s is a symlink; refusing to prepare it as root", p)
			}
			if fi, err = os.Stat(p); err != nil {
				return created, err
			}
		}
		if !fi.IsDir() {
			return created, fmt.Errorf("%s is not a directory", p)
		}
	}
	return created, nil
}

func within(home, p string) bool {
	rel, err := filepath.Rel(home, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func homeOwner(home string, getenv func(string) string) (uid, gid int, ok bool) {
	if u, err := strconv.Atoi(getenv("SUDO_UID")); err == nil && u != 0 {
		g, err := strconv.Atoi(getenv("SUDO_GID"))
		if err != nil {
			g = -1
		}
		return u, g, true
	}
	for _, p := range []string{home, filepath.Dir(home)} {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st, isUnix := fi.Sys().(*syscall.Stat_t); isUnix && st.Uid != 0 {
			return int(st.Uid), int(st.Gid), true
		}
		return 0, 0, false
	}
	return 0, 0, false
}
