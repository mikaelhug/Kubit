package repo

import (
	"bytes"
	stdx509 "crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/sops"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"k8s.io/client-go/tools/clientcmd"
)

const spec = `apiVersion: kubit.dev/v1
kind: Cluster
metadata: {name: lab}
spec:
  controlPlane: {endpoint: "https://10.0.0.10:6443"}
  nodes:
    - {hostname: cp-01, ip: 10.0.0.10, role: controlplane, installDisk: {path: /dev/sda}}
    - {hostname: w-01, ip: 10.0.0.11, role: worker, installDisk: {path: /dev/sda}}
`

func initRepo(t *testing.T) (string, *age.X25519Identity) {
	t.Helper()
	c, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "lab")
	if _, err := Init(dir, c, []string{id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	return dir, id
}

func TestInitNeverWritesSecretsInClearOrOverAnExistingRepo(t *testing.T) {
	dir, id := initRepo(t)
	enc, err := os.ReadFile(filepath.Join(dir, SecretsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !sops.Encrypted(enc) || bytes.Contains(enc, []byte("AGE-SECRET-KEY")) || bytes.Contains(enc, []byte("CERTIFICATE")) {
		t.Fatalf("secrets are not sealed:\n%s", enc)
	}
	ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, p := range []string{"talosconfig", "kubeconfig"} {
		if !strings.Contains(string(ign), p+"\n") {
			t.Errorf(".gitignore lacks %s:\n%s", p, ign)
		}
	}
	c, _ := config.Parse([]byte(spec))
	if _, err := Init(dir, c, []string{id.Recipient().String()}); err == nil {
		t.Error("a second init must refuse")
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := LoadWith(dir, []age.Identity{other}); err == nil {
		t.Error("another key must not open the secrets")
	}
}

func TestDerivedCredentialsCarryTheRolesTalosAndKubernetesRequire(t *testing.T) {
	dir, id := initRepo(t)
	r, err := LoadWith(dir, []age.Identity{id})
	if err != nil {
		t.Fatal(err)
	}
	tc, err := r.Talosconfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.FromBytes(tc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cfg.Contexts[cfg.Context]
	if ctx == nil {
		t.Fatalf("talosconfig has no context %q", cfg.Context)
	}
	verify(t, ctx.Crt, r.Secrets.Bundle.Certs.OS.Crt, "os:admin")
	kc, err := r.Kubeconfig()
	if err != nil {
		t.Fatal(err)
	}
	k, err := clientcmd.Load(kc)
	if err != nil {
		t.Fatal(err)
	}
	verify(t, k.AuthInfos["admin@lab"].ClientCertificateData, r.Secrets.Bundle.Certs.K8s.Crt, "system:masters")
}

func verify(t *testing.T, leafPEM any, caPEM []byte, org string) {
	t.Helper()
	var raw []byte
	switch v := leafPEM.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	}
	if b, err := decodeB64(raw); err == nil {
		raw = b
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		t.Fatalf("no PEM in %q", raw)
	}
	leaf, err := stdx509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := stdx509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	if _, err := leaf.Verify(stdx509.VerifyOptions{Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("%s does not chain: %v", leaf.Subject, err)
	}
	if len(leaf.Subject.Organization) == 0 || leaf.Subject.Organization[0] != org {
		t.Errorf("organization %v, want %s", leaf.Subject.Organization, org)
	}
}

func decodeB64(b []byte) ([]byte, error) {
	return base64.StdEncoding.DecodeString(string(b))
}

func TestSopsCLIOpensTheSecrets(t *testing.T) {
	bin, err := exec.LookPath("sops")
	if err != nil {
		t.Skip("sops not installed")
	}
	dir, id := initRepo(t)
	cmd := exec.Command(bin, "-d", filepath.Join(dir, SecretsFile))
	cmd.Env = append(os.Environ(), "SOPS_AGE_KEY="+id.String(), "SOPS_AGE_KEY_FILE=", "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("statePassphrase:")) || !bytes.Contains(out, []byte("AGE-SECRET-KEY-")) {
		t.Fatalf("sops -d: %v\n%s", err, out)
	}
}
