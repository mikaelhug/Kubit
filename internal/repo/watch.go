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
	"github.com/mikael/kubit/internal/sops"
)

const settle = 300 * time.Millisecond

type Events struct {
	Changed func(dir string)
	Secrets func(dir string)
	Git     func(dir string)
}

func Watch(ctx context.Context, dirs []string, ev Events) error {
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
	var mu sync.Mutex
	gitDirs := map[string]string{}
	addGit := func(dir, root string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				mu.Lock()
				gitDirs[p] = root
				mu.Unlock()
				_ = w.Add(p)
			}
			return nil
		})
	}
	watchGit := func(root string) {
		gd, err := git(ctx, root, "rev-parse", "--absolute-git-dir")
		if err != nil || ev.Git == nil {
			return
		}
		gd = strings.TrimSpace(gd)
		mu.Lock()
		gitDirs[gd] = root
		mu.Unlock()
		_ = w.Add(gd)
		addGit(filepath.Join(gd, "refs"), root)
	}
	for _, d := range dirs {
		add(d)
		watchGit(d)
	}
	timers := map[string]*time.Timer{}
	fire := func(key, dir string, f func(string)) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := timers[key]; ok {
			t.Reset(settle)
			return
		}
		timers[key] = time.AfterFunc(settle, func() {
			mu.Lock()
			delete(timers, key)
			mu.Unlock()
			f(dir)
		})
	}
	gitRoot := func(path string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		root, ok := gitDirs[filepath.Dir(path)]
		return root, ok
	}
	go func() {
		defer w.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-w.Events:
				if !ok {
					return
				}
				if root, ok := gitRoot(e.Name); ok {
					if e.Has(fsnotify.Create) {
						addGit(e.Name, root)
					}
					fire("git:"+root, root, ev.Git)
					continue
				}
				root := owner(dirs, e.Name)
				if root == "" {
					continue
				}
				if base := filepath.Base(e.Name); e.Has(fsnotify.Create) {
					switch {
					case base == ".git" && filepath.Dir(e.Name) == root:
						watchGit(root)
						fire("git:"+root, root, ev.Git)
					case !slices.Contains(skipDirs, base):
						add(e.Name)
					}
				}
				spec, secrets := relevant(root, e.Name)
				if spec {
					fire(root, root, ev.Changed)
				}
				if secrets && ev.Secrets != nil {
					fire("secrets:"+root, root, ev.Secrets)
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

func relevant(root, path string) (spec, secrets bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, false
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	dir := !strings.Contains(base, ".")
	spec = rel == ClusterFile || rel == SecretsFile || rel == sops.ConfigFile || strings.HasPrefix(rel, SnapshotDir+"/") || dir
	secrets = base == sops.ConfigFile || IsSecretFile(rel) || IsKustomization(rel) || dir
	return spec, secrets
}
