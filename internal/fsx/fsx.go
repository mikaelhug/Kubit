package fsx

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var locks sync.Map

func Lock(path string) func() {
	m, _ := locks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func WriteFile(path string, data []byte, perm os.FileMode) error {
	return WriteStream(path, perm, Bytes(data))
}

func Bytes(data []byte) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.Copy(w, bytes.NewReader(data))
		return err
	}
}

func WriteStream(path string, perm os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := write(f); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func SweepParts(dir string, age time.Duration) error {
	cutoff := time.Now().Add(-age)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isPart(d.Name()) {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func isPart(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".part")
}
