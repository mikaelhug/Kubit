package libvirt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var held []net.Conn
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
	})
	return ln.Addr().String()
}

func TestHandshakeHonoursContextAndDeadline(t *testing.T) {
	addr := silentListener(t)
	cfg := &ssh.ClientConfig{User: "kubit", HostKeyCallback: ssh.InsecureIgnoreHostKey()}

	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := handshake(ctx, raw, "127.0.0.1", cfg); err == nil {
		t.Fatal("a server that never speaks SSH must fail the handshake")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("cancelled handshake took %s", d)
	}

	defer func(d time.Duration) { handshakeTimeout = d }(handshakeTimeout)
	handshakeTimeout = 200 * time.Millisecond
	raw, err = net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	if _, err := handshake(context.Background(), raw, "127.0.0.1", cfg); err == nil {
		t.Fatal("a silent server must hit the handshake deadline")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("handshake deadline took %s", d)
	}
}

func TestPutScriptReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "network.xml")
	run := func(content string) {
		t.Helper()
		if out, err := exec.Command("sh", "-c", putScript(path, []byte(content), "640")).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run("<network>'quoted' $HOME</network>")
	run("<network>second</network>")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "<network>second</network>\n" {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temporary file left behind: %v", entries)
	}
	if err := exec.Command("sh", "-c", putScript(filepath.Join(dir, "sub", "x"), []byte("x"), "bad-mode")).Run(); err == nil {
		t.Error("a failed chmod must fail the put")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "x")); !os.IsNotExist(err) {
		t.Error("a failed put must not leave the target")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "x.kubit-tmp")); !os.IsNotExist(err) {
		t.Error("a failed put must remove its temporary file")
	}
}

func fakeHost(t *testing.T, bins map[string]string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range bins {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + ":/usr/bin:/bin", "D=" + dir}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go serveExec(ctx, raw, cfg, env)
		}
	}()
	conn, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "kubit", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		ln.Close()
		cancel()
	})
	return &Client{Host: "127.0.0.1", conn: conn}, dir
}

func serveExec(ctx context.Context, raw net.Conn, cfg *ssh.ServerConfig, env []string) {
	_, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, reqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range reqs {
				if req.Type != "exec" {
					_ = req.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &p)
				_ = req.Reply(true, nil)
				cmd := exec.CommandContext(ctx, "sh", "-c", p.Command)
				cmd.Env, cmd.Stdout, cmd.Stderr = env, ch, ch.Stderr()
				var status uint32
				var exit *exec.ExitError
				if err := cmd.Run(); errors.As(err, &exit) {
					status = uint32(exit.ExitCode())
				} else if err != nil {
					status = 127
				}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

func TestRebootAndShutdownWaitForARunningUpgrade(t *testing.T) {
	bins := map[string]string{
		"sudo":      `shift; exec "$@"`,
		"sleep":     `true`,
		"systemctl": `echo "systemctl $*" >> "$D/log"; n=$(cat "$D/active" 2>/dev/null || echo 0); echo $((n+1)) > "$D/active"; [ "$n" -lt 2 ]`,
		"nohup":     `echo "nohup $*" >> "$D/log"`,
		"virsh":     `echo "virsh $*" >> "$D/log"`,
	}
	for name, run := range map[string]func(*Client) error{
		"reboot":   func(c *Client) error { return c.Reboot(t.Context()) },
		"shutdown": func(c *Client) error { return c.ShutdownVMs(t.Context(), time.Second) },
	} {
		c, dir := fakeHost(t, bins)
		if err := run(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var log []byte
		var lines []string
		for deadline := time.Now().Add(2 * time.Second); len(lines) < 4 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			log, _ = os.ReadFile(filepath.Join(dir, "log"))
			lines = strings.Split(strings.TrimSpace(string(log)), "\n")
		}
		if len(lines) < 4 || strings.Count(string(log), "systemctl is-active -q kubit-upgrade.service") != 3 {
			t.Fatalf("%s: must poll the upgrade unit until it stops:\n%s", name, log)
		}
		for _, l := range lines[:3] {
			if !strings.HasPrefix(l, "systemctl is-active") {
				t.Errorf("%s: acted before the upgrade finished:\n%s", name, log)
			}
		}
	}

	c, dir := fakeHost(t, map[string]string{"sudo": `shift; exec "$@"`, "sleep": `/bin/sleep 0.05`, "systemctl": `true`, "nohup": `echo "nohup $*" >> "$D/log"`})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := c.Reboot(ctx); err == nil || !strings.Contains(err.Error(), "an upgrade is still running") {
		t.Errorf("a never-ending upgrade blocks the reboot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "log")); err == nil {
		t.Error("no reboot while the upgrade runs")
	}
}
