//go:build integration

package cluster

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/tofu"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const bareCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: it }
spec:
  controlPlane: { endpoint: "https://10.0.0.10:6443" }
  nodes:
    - { hostname: cp-01, ip: 10.0.0.10, role: controlplane, installDisk: { path: /dev/sda } }
  platform:
    metricsServer: { enabled: false }
    certManager: { enabled: false }
    flux: { enabled: false }
`

func TestPlatformStateStaysEncryptedAndAPlanWritesNothing(t *testing.T) {
	kubeconfig := os.Getenv("KUBIT_TEST_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBIT_TEST_KUBECONFIG names a throwaway cluster")
	}
	kc, err := os.ReadFile(kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse([]byte(bareCluster))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m := NewManager(testStore(t), filepath.Join(os.TempDir(), "kubit-integration-home"))
	repoDir := t.TempDir()
	d := &Desired{Dir: repoDir, Cluster: c, Kubeconfig: kc, Passphrase: "integration-passphrase-0123456789", RepoState: filepath.Join(repoDir, "state", "platform.tfstate")}
	client, err := m.KubeClientFor(c.Metadata.Name, kc)
	if err != nil {
		t.Fatal(err)
	}
	secrets := client.CoreV1().Secrets(tofu.StateNamespace)
	_ = secrets.Delete(ctx, tofu.StateSecret, metav1.DeleteOptions{})
	t.Cleanup(func() { _ = secrets.Delete(context.Background(), tofu.StateSecret, metav1.DeleteOptions{}) })

	bin, err := m.tofuBin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := tofu.New(bin, filepath.Join(m.Home, "plugins"), nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Passphrase = d.Passphrase
	if err := legacy.WriteKubeconfig(kc); err != nil {
		t.Fatal(err)
	}
	if err := tofu.Render(legacy.Dir, c, legacy.Kubeconfig, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(d.RepoState), 0o755); err != nil {
		t.Fatal(err)
	}
	apply := func(r *tofu.Runner) {
		t.Helper()
		if _, err := r.Plan(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Apply(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.InitFrom(ctx, d.RepoState); err != nil {
		t.Fatal(err)
	}
	apply(legacy)
	legacy.Close()
	if b, err := os.ReadFile(d.RepoState); err != nil || !strings.Contains(string(b), "encrypted_data") {
		t.Fatalf("the repo state an older Kubit wrote is encrypted: %v\n%s", err, b)
	}

	r, move, err := m.openPlatform(ctx, d, c, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if move != stateFromRepo {
		t.Fatalf("a repo state with none in the cluster is moved: %v", move)
	}
	if _, err := os.Stat(d.RepoState); err != nil {
		t.Fatal("a plan moves nothing")
	}
	if _, err := secrets.Get(ctx, tofu.StateSecret, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("a plan writes no state into the cluster: %v", err)
	}

	r, _, err = m.openPlatform(ctx, d, c, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	apply(r)
	r.Close()
	if _, err := os.Stat(filepath.Dir(d.RepoState)); !os.IsNotExist(err) {
		t.Errorf("the moved state leaves the repo: %v", err)
	}
	s, err := secrets.Get(ctx, tofu.StateSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(s.Data["tfstate"]))
	if err != nil {
		t.Fatal(err)
	}
	state, _ := io.ReadAll(zr)
	if !strings.Contains(string(state), "encrypted_data") || strings.Contains(string(state), `"outputs"`) {
		t.Errorf("the state in the cluster is encrypted:\n%.300s", state)
	}
}
