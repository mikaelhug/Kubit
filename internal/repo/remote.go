package repo

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	r, ok := parseRemote(raw)
	if !ok {
		return ""
	}
	path := strings.TrimSuffix(strings.Trim(r.path, "/"), ".git")
	return strings.ToLower(r.host) + "/" + strings.ToLower(path)
}

type remote struct {
	scheme, user, host, port, path string
}

func parseRemote(raw string) (remote, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return remote{}, false
	}
	var r remote
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		r = remote{scheme: u.Scheme, user: u.User.Username(), host: u.Hostname(), port: u.Port(), path: u.Path}
	} else if at, rest, ok := strings.Cut(s, ":"); ok && !strings.Contains(at, "/") {
		user, h, found := strings.Cut(at, "@")
		if !found {
			user, h = "", at
		}
		r = remote{scheme: "ssh", user: user, host: h, path: rest}
	} else {
		return remote{}, false
	}
	if r.scheme == "ssh" {
		if host, port := sshHost(r.host); host != "" {
			r.host = host
			if r.port == "" && port != "22" {
				r.port = port
			}
		}
	}
	return r, true
}

func FluxURL(raw string) (string, bool) {
	r, ok := parseRemote(raw)
	if !ok {
		return "", false
	}
	if r.scheme != "ssh" {
		return raw, true
	}
	return sshURL(r), true
}

func SSHForm(raw string) (string, bool) {
	r, ok := parseRemote(raw)
	if !ok {
		return "", false
	}
	r.port = ""
	return sshURL(r), true
}

func sshURL(r remote) string {
	user := r.user
	if user == "" {
		user = "git"
	}
	host := r.host
	if r.port != "" {
		host = net.JoinHostPort(r.host, r.port)
	}
	return "ssh://" + user + "@" + host + "/" + strings.TrimPrefix(r.path, "/")
}

var sshHosts sync.Map

func sshHost(alias string) (string, string) {
	if v, ok := sshHosts.Load(alias); ok {
		hp := v.([2]string)
		return hp[0], hp[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ssh", "-G", alias).Output()
	if err != nil {
		return "", ""
	}
	var host, port string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "hostname":
			host = v
		case "port":
			port = v
		}
	}
	sshHosts.Store(alias, [2]string{host, port})
	return host, port
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
