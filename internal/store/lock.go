package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Lock holds the single-instance lock of a KUBIT_HOME while a daemon runs.
type Lock struct{ f *os.File }

// LockHome takes an exclusive advisory lock on <dir>/serve.lock and records the pid;
// a second daemon on the same home fails with the holder's pid.
func LockHome(dir string) (*Lock, error) {
	path := filepath.Join(dir, "serve.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		b, _ := os.ReadFile(path)
		f.Close()
		pid := string(b)
		if pid == "" {
			pid = "unknown"
		}
		return nil, fmt.Errorf("another kubit serve is running on this KUBIT_HOME (pid %s)", pid)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return &Lock{f: f}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
