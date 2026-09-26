package libvirt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/factory"
	"github.com/mikael/kubit/internal/labhost"
	"golang.org/x/crypto/ssh"
)

type Client struct {
	Host string
	cfg  *ssh.ClientConfig
	conn *ssh.Client
}

func GenerateKey() (priv []byte, pub string, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(privKey, "kubit")
	if err != nil {
		return nil, "", err
	}
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(block), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " kubit", nil
}

func Dial(ctx context.Context, host string, privPEM []byte) (*Client, error) {
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		return nil, err
	}
	c := &Client{Host: host, cfg: &ssh.ClientConfig{User: User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second}}
	if err := c.dial(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) dial(ctx context.Context) error {
	d := net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, "22"))
	if err != nil {
		return err
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, c.Host, c.cfg)
	if err != nil {
		raw.Close()
		return err
	}
	c.conn = ssh.NewClient(conn, chans, reqs)
	return nil
}

func (c *Client) reconnect(ctx context.Context, within time.Duration) error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	deadline := time.Now().Add(within)
	for {
		if err := c.dial(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("could not reconnect to %s within %s", c.Host, within)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) Run(ctx context.Context, cmd string) (string, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out, errb bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errb
	done := make(chan error, 1)
	go func() { done <- sess.Run("sudo -n sh -c " + shellQuote(cmd)) }()
	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		select {
		case <-done:
			return out.String(), ctx.Err()
		case <-time.After(runDrain):
			return "", ctx.Err()
		}
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%s: %w: %s", firstWord(cmd), err, strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	}
}

const runDrain = 5 * time.Second

func (c *Client) Put(ctx context.Context, path string, content []byte, mode string) error {
	_, err := c.Run(ctx, fmt.Sprintf("mkdir -p $(dirname %s) && cat > %s <<'KUBIT_EOF'\n%s\nKUBIT_EOF\nchmod %s %s", shellQuote(path), shellQuote(path), content, mode, shellQuote(path)))
	return err
}

func (c *Client) Capacity(ctx context.Context) (labhost.Capacity, error) {
	out, err := c.Run(ctx, `echo "cpus=$(nproc)"; echo "mem=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo)"; mkdir -p `+vmDir+`; echo "diskbytes=$(df -B1 --output=avail `+vmDir+` | tail -1 | tr -dc 0-9)"; echo "kvm=$( [ -c /dev/kvm ] && echo yes || echo no)"; echo "kernel=$(uname -r)"; echo "arch=$(uname -m)"; echo "host=$(hostname)"; echo "libvirt=$(virsh version --daemon 2>/dev/null | awk '/Using library/{print $NF}')"; echo "bridge=$(ip -o link show type bridge | awk -F': ' '{print $2}' | grep -xF br0 || ip -o link show type bridge | awk -F': ' '{print $2}' | head -1)"; echo "ready=$( [ -f /var/lib/kubit/READY ] && echo yes || echo no)"`)
	if err != nil {
		return labhost.Capacity{}, err
	}
	kv := keyValues(out)
	cp := labhost.Capacity{Kernel: kv["kernel"], Libvirt: kv["libvirt"], Hostname: kv["host"], Bridge: kv["bridge"], KVM: kv["kvm"] == "yes", Ready: kv["ready"] == "yes", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	cp.CPUs, _ = strconv.Atoi(kv["cpus"])
	cp.MemMiB, _ = strconv.Atoi(kv["mem"])
	if b, err := strconv.ParseInt(kv["diskbytes"], 10, 64); err == nil {
		cp.DiskGiB = int(b / (1 << 30))
	}
	switch kv["arch"] {
	case "x86_64":
		cp.Arch = "amd64"
	case "aarch64":
		cp.Arch = "arm64"
	default:
		cp.Arch = kv["arch"]
	}
	return cp, nil
}

func (c *Client) EnsureTalosBoot(ctx context.Context, f *factory.Client, schematic, version, arch string) (labhost.Boot, error) {
	name, err := labhost.BootCacheName(version, schematic)
	if err != nil {
		return labhost.Boot{}, err
	}
	dir := bootDir + "/" + name
	kernel, initrd := dir+"/kernel-"+arch, dir+"/initramfs-"+arch+".xz"
	_, err = c.Run(ctx, fmt.Sprintf(`set -e
mkdir -p %[1]s && cd %[1]s
get() { [ -s "$1" ] && return 0; curl -fSL --retry 3 -o "$1.part" "$2" && mv -f "$1.part" "$1" || { rm -f "$1.part"; echo "download failed: $2" >&2; return 1; }; [ -s "$1" ] || { echo "empty after download: $1" >&2; return 1; }; }
get kernel-%[2]s %[3]s
get initramfs-%[2]s.xz %[4]s`, dir, arch, f.KernelURL(schematic, version, arch), f.InitramfsURL(schematic, version, arch)))
	return labhost.Boot{Kernel: kernel, Initrd: initrd}, err
}

func connDropped(err error) bool {
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) {
		return true
	}
	s := err.Error()
	for _, sig := range []string{"use of closed network connection", "connection reset", "broken pipe", "io: read/write on closed pipe"} {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return s
	}
	if len(f[0]) > 40 {
		return f[0][:40]
	}
	return f[0]
}

var (
	_ labhost.Driver  = (*Client)(nil)
	_ labhost.Updater = (*Client)(nil)
	_ labhost.Router  = (*Client)(nil)
)
