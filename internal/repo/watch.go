package repo

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const settle = 300 * time.Millisecond

func Watch(ctx context.Context, dirs []string, changed func(dir string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	add := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if slices.Contains(skipDirs, d.Name()) && p != root {
				return filepath.SkipDir
			}
			_ = w.Add(p)
			return nil
		})
	}
	for _, d := range dirs {
		add(d)
	}
	var mu sync.Mutex
	timers := map[string]*time.Timer{}
	fire := func(dir string) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := timers[dir]; ok {
			t.Reset(settle)
			return
		}
		timers[dir] = time.AfterFunc(settle, func() {
			mu.Lock()
			delete(timers, dir)
			mu.Unlock()
			changed(dir)
		})
	}
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				root := owner(dirs, ev.Name)
				if root == "" {
					continue
				}
				if ev.Has(fsnotify.Create) {
					add(ev.Name)
				}
				if relevant(root, ev.Name) {
					fire(root)
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

func owner(dirs []string, path string) string {
	best := ""
	for _, d := range dirs {
		if rel, err := filepath.Rel(d, path); err == nil && !strings.HasPrefix(rel, "..") && len(d) > len(best) {
			best = d
		}
	}
	return best
}

func relevant(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	return rel == ClusterFile || rel == SecretsFile || base == ".sops.yaml" || IsSecretFile(rel) || !strings.Contains(base, ".")
}
