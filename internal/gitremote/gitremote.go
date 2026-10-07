package gitremote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Refusal struct{ Msg string }

func (r *Refusal) Error() string { return r.Msg }

type Key struct {
	Public      string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

type Host struct {
	Host        string `json:"host"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

const timeout = 15 * time.Second

var scanAlgorithms = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512}

func IsSSH(rawURL string) bool { return strings.HasPrefix(rawURL, "ssh://") }

func NewKey(comment string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(block)), nil
}

func PublicKey(key string) (Key, error) {
	s, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil {
		return Key{}, fmt.Errorf("flux.deployKey: %w", err)
	}
	return Key{Public: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))), Fingerprint: ssh.FingerprintSHA256(s.PublicKey())}, nil
}

func Hosts(knownHosts string) []Host {
	var out []Host
	for _, e := range entries(knownHosts) {
		out = append(out, Host{Host: strings.Join(e.hosts, ","), Type: e.key.Type(), Fingerprint: ssh.FingerprintSHA256(e.key)})
	}
	return out
}

func Pinned(hosts []Host, rawURL string) bool {
	addr, err := HostOf(rawURL)
	if err != nil {
		return false
	}
	name := knownhosts.Normalize(addr)
	return slices.ContainsFunc(hosts, func(h Host) bool { return slices.Contains(strings.Split(h.Host, ","), name) })
}

func HostOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("%q has no host", rawURL)
	}
	port := u.Port()
	if port == "" {
		port = "22"
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

func ScanHosts(ctx context.Context, rawURL string) (string, error) {
	addr, err := HostOf(rawURL)
	if err != nil {
		return "", err
	}
	var lines []string
	var errs []error
	for _, algo := range scanAlgorithms {
		k, err := hostKey(ctx, addr, algo)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		lines = append(lines, knownhosts.Line([]string{knownhosts.Normalize(addr)}, k))
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("scan %s: %w", addr, errors.Join(errs...))
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func hostKey(ctx context.Context, addr, algo string) (ssh.PublicKey, error) {
	conn, err := dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User:              "git",
		HostKeyAlgorithms: []string{algo},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = k
			return errors.New("host key read")
		},
	}
	_, _, _, err = ssh.NewClientConn(conn, addr, cfg)
	if got != nil {
		return got, nil
	}
	return nil, err
}

func Probe(ctx context.Context, rawURL, branch, key, knownHosts string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	addr, err := HostOf(rawURL)
	if err != nil {
		return err
	}
	host := u.Hostname()
	signer, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil {
		return &Refusal{"flux.deployKey: " + err.Error()}
	}
	want := keysFor(knownHosts, addr)
	if len(want) == 0 {
		return &Refusal{fmt.Sprintf("flux.knownHosts has no key for %s; rescan the host keys", host)}
	}
	cfg := &ssh.ClientConfig{
		User:              u.User.Username(),
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyAlgorithms: algorithms(want),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if slices.ContainsFunc(want, func(w ssh.PublicKey) bool { return bytes.Equal(w.Marshal(), k.Marshal()) }) {
				return nil
			}
			return &Refusal{fmt.Sprintf("%s's host key is not in flux.knownHosts; rescan the host keys", host)}
		},
	}
	conn, err := dial(ctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		var r *Refusal
		if errors.As(err, &r) {
			return r
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return &Refusal{fmt.Sprintf("%s refused flux.deployKey; add it to the repository's deploy keys", host)}
		}
		return err
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	if err := sess.Start("git-upload-pack " + shellQuote(u.Path)); err != nil {
		return err
	}
	refs, readErr := readRefs(bufio.NewReader(stdout))
	_, _ = stdin.Write([]byte("0000"))
	_ = stdin.Close()
	_ = sess.Wait()
	if readErr != nil && len(refs) == 0 {
		if msg := firstLine(stderr.String()); msg != "" {
			return &Refusal{fmt.Sprintf("%s: %s", rawURL, msg)}
		}
		return readErr
	}
	return hasBranch(rawURL, refs, branch)
}

func infoRefs(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(rawURL, "/")+"/info/refs?service=git-upload-pack", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "git/kubit")
	return (&http.Client{Timeout: timeout}).Do(req)
}

func private(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound
}

func Private(ctx context.Context, rawURL string) bool {
	resp, err := infoRefs(ctx, rawURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return private(resp.StatusCode)
}

func ProbeHTTPS(ctx context.Context, rawURL, branch string) error {
	resp, err := infoRefs(ctx, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case private(resp.StatusCode):
		return &Refusal{fmt.Sprintf("%s is private or missing; a private repository needs its ssh:// URL and a deploy key", rawURL)}
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	r := bufio.NewReader(io.LimitReader(resp.Body, 8<<20))
	if _, err := readRefs(r); err != nil {
		return err
	}
	refs, err := readRefs(r)
	if err != nil {
		return err
	}
	return hasBranch(rawURL, refs, branch)
}

func hasBranch(rawURL string, refs []string, branch string) error {
	if slices.Contains(refs, "refs/heads/"+branch) {
		return nil
	}
	return &Refusal{fmt.Sprintf("%s has no branch %s", rawURL, branch)}
}

func readRefs(r *bufio.Reader) ([]string, error) {
	var refs []string
	for {
		var size [4]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return refs, err
		}
		n, err := strconv.ParseUint(string(size[:]), 16, 16)
		if err != nil {
			return refs, fmt.Errorf("pkt-line length %q", size)
		}
		if n == 0 {
			return refs, nil
		}
		if n < 4 {
			return refs, fmt.Errorf("pkt-line length %d", n)
		}
		line := make([]byte, n-4)
		if _, err := io.ReadFull(r, line); err != nil {
			return refs, err
		}
		s, _, _ := strings.Cut(strings.TrimSuffix(string(line), "\n"), "\x00")
		if _, ref, ok := strings.Cut(s, " "); ok {
			refs = append(refs, ref)
		}
	}
}

type entry struct {
	hosts []string
	key   ssh.PublicKey
}

func entries(knownHosts string) []entry {
	var out []entry
	rest := []byte(knownHosts)
	for len(rest) > 0 {
		marker, hosts, key, _, r, err := ssh.ParseKnownHosts(rest)
		if err != nil {
			break
		}
		if marker == "" {
			out = append(out, entry{hosts, key})
		}
		rest = r
	}
	return out
}

func keysFor(knownHosts, addr string) []ssh.PublicKey {
	name := knownhosts.Normalize(addr)
	var out []ssh.PublicKey
	for _, e := range entries(knownHosts) {
		if slices.Contains(e.hosts, name) {
			out = append(out, e.key)
		}
	}
	return out
}

func algorithms(keys []ssh.PublicKey) []string {
	var out []string
	for _, k := range keys {
		if k.Type() == ssh.KeyAlgoRSA {
			out = append(out, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256)
			continue
		}
		out = append(out, k.Type())
	}
	return out
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return conn, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
