package repo

import (
	"bufio"
	"context"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
)

func Remotes(ctx context.Context, dir string) []string {
	out, err := git(ctx, dir, "remote", "-v")
	if err != nil {
		return nil
	}
	var urls []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && !slices.Contains(urls, f[1]) {
			urls = append(urls, f[1])
		}
	}
	return urls
}

func SameRemote(a, b string) bool {
	na, nb := normalRemote(a), normalRemote(b)
	return na != "" && na == nb
}

func normalRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	host, path := "", ""
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if at, rest, ok := strings.Cut(s, ":"); ok && !strings.Contains(at, "/") {
		_, h, found := strings.Cut(at, "@")
		if !found {
			h = at
		}
		host, path = h, rest
	} else {
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return strings.ToLower(host) + "/" + strings.ToLower(path)
}

func FileStatus(ctx context.Context, dir string) map[string]string {
	st := map[string]string{}
	prefix, err := git(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return st
	}
	prefix = strings.TrimSpace(prefix)
	out, err := git(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return st
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		code, path := strings.TrimSpace(e[:2]), e[3:]
		if strings.HasPrefix(code, "R") || strings.HasPrefix(code, "C") {
			i++
		}
		rel, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		st[filepath.ToSlash(rel)] = code
	}
	return st
}
