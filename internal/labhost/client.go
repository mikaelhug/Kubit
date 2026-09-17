package labhost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client runs commands on a lab host as the kubit user over SSH.
type Client struct {
	Host string
	conn *ssh.Client
}

// GenerateKey mints Kubit's ed25519 key pair; returns the PEM private key and the
// authorized_keys line.
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

// Dial connects with the private key; the host key is not pinned (first contact is
// on the LAN right after Kubit installed the host) but recorded by the caller.
func Dial(ctx context.Context, host string, privPEM []byte) (*Client, error) {
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{User: User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second}
	d := net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "22"))
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(raw, host, cfg)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return &Client{Host: host, conn: ssh.NewClient(c, chans, reqs)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Run executes a command (through sudo for libvirt/file work) and returns stdout.
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
		return out.String(), ctx.Err()
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%s: %w: %s", firstWord(cmd), err, strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	}
}

// Put writes a file on the host (small files: preseed leftovers, domain XML).
func (c *Client) Put(ctx context.Context, path string, content []byte, mode string) error {
	_, err := c.Run(ctx, fmt.Sprintf("mkdir -p $(dirname %s) && cat > %s <<'KUBIT_EOF'\n%s\nKUBIT_EOF\nchmod %s %s", shellQuote(path), shellQuote(path), content, mode, shellQuote(path)))
	return err
}

// Capacity is what the host can give to VMs.
type Capacity struct {
	CPUs      int    `json:"cpus"`
	MemMiB    int    `json:"memMiB"`
	DiskGiB   int    `json:"diskGiB"` // free under VMDir
	KVM       bool   `json:"kvm"`
	Kernel    string `json:"kernel"`
	Libvirt   string `json:"libvirt"`
	Hostname  string `json:"hostname"`
	Arch      string `json:"arch"`
	Bridge    string `json:"bridge"`
	Ready     bool   `json:"ready"`
	CheckedAt string `json:"checkedAt"`
}

// Capacity reads CPU, memory, free disk and the virtualisation prerequisites.
func (c *Client) Capacity(ctx context.Context) (Capacity, error) {
	out, err := c.Run(ctx, `echo "cpus=$(nproc)"; echo "mem=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo)"; mkdir -p `+VMDir+`; echo "disk=$(df -BG --output=avail `+VMDir+` | tail -1 | tr -dc 0-9)"; echo "kvm=$( [ -c /dev/kvm ] && echo yes || echo no)"; echo "kernel=$(uname -r)"; echo "arch=$(uname -m)"; echo "host=$(hostname)"; echo "libvirt=$(virsh version --daemon 2>/dev/null | awk '/Using library/{print $NF}')"; echo "bridge=$(ip -o link show type bridge | awk -F': ' '{print $2}' | head -1)"; echo "ready=$( [ -f /var/lib/kubit/READY ] && echo yes || echo no)"`)
	if err != nil {
		return Capacity{}, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "="); i > 0 {
			kv[line[:i]] = strings.TrimSpace(line[i+1:])
		}
	}
	cp := Capacity{Kernel: kv["kernel"], Libvirt: kv["libvirt"], Hostname: kv["host"], Bridge: kv["bridge"], KVM: kv["kvm"] == "yes", Ready: kv["ready"] == "yes", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	cp.CPUs, _ = strconv.Atoi(kv["cpus"])
	cp.MemMiB, _ = strconv.Atoi(kv["mem"])
	cp.DiskGiB, _ = strconv.Atoi(kv["disk"])
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

// EnsureTalosBoot puts the Talos kernel and initramfs for a schematic/version on the
// host (downloaded by the host from the Image Factory) and returns their paths.
func (c *Client) EnsureTalosBoot(ctx context.Context, factoryURL, schematic, version, arch string) (kernel, initrd string, err error) {
	dir := fmt.Sprintf("%s/%s-%s", BootDir, version, schematic[:12])
	kernel, initrd = dir+"/kernel-"+arch, dir+"/initramfs-"+arch+".xz"
	_, err = c.Run(ctx, fmt.Sprintf(`mkdir -p %s && cd %s && [ -s kernel-%s ] || curl -fsSL -o kernel-%s %s/image/%s/%s/kernel-%s; [ -s initramfs-%s.xz ] || curl -fsSL -o initramfs-%s.xz %s/image/%s/%s/initramfs-%s.xz; ls -l`, dir, dir, arch, arch, factoryURL, schematic, version, arch, arch, arch, factoryURL, schematic, version, arch))
	return kernel, initrd, err
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
