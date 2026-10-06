package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/config"
)

const sampleSpec = `apiVersion: kubit.dev/v1
kind: Cluster
metadata:
  name: home
spec:
  # the first box under the desk
  nodes:
    - hostname: home-cp-01
      ip: 192.168.5.10
      role: controlplane
      installDisk: {path: /dev/nvme0n1}
`

func TestNodeEditsKeepCommentsAndRefuseAnInvalidSpec(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ClusterFile)
	if err := os.WriteFile(path, []byte(sampleSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	n := config.Node{Hostname: "home-worker-01", IP: "192.168.5.159", MAC: "98:e7:f4:be:4d:9e", Role: config.RoleWorker, Arch: config.ArchAMD64, InstallDisk: config.InstallDisk{Path: "/dev/nvme0n1"}}
	if err := AddNodes(dir, "stale", n); err != ErrStale {
		t.Fatalf("an edit of an older copy is refused: %v", err)
	}
	if err := AddNodes(dir, Fingerprint([]byte(sampleSpec)), n); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	c, err := config.Parse(b)
	if err != nil || len(c.Spec.Nodes) != 2 || c.Spec.Nodes[1].MAC != "98:e7:f4:be:4d:9e" {
		t.Fatalf("after add: %v\n%s", err, b)
	}
	if !strings.Contains(string(b), "# the first box under the desk") {
		t.Errorf("comments must survive an edit:\n%s", b)
	}
	if err := AddNodes(dir, Fingerprint(b), config.Node{Hostname: "bad", IP: "192.168.5.11", Role: "gpu"}); err == nil {
		t.Error("an entry that breaks cluster.yaml must not be written")
	}
}

func TestMovingTheEndpointNodeRequiresMovingTheEndpoint(t *testing.T) {
	dir := t.TempDir()
	spec := "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  controlPlane: {endpoint: \"https://192.168.5.240:6443\"}\n  network: {nameservers: [192.168.5.1]}\n  nodes:\n    - {hostname: cp-01, ip: 192.168.5.240, role: controlplane, installDisk: {path: /dev/sda}}\n"
	if err := os.WriteFile(filepath.Join(dir, ClusterFile), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	nn := &config.NodeNetwork{Addresses: []string{"192.168.5.51/24"}, Gateway: "192.168.5.1"}
	hash := Fingerprint([]byte(spec))
	if err := SetNodeNetwork(dir, hash, "cp-01", nn, ""); err == nil {
		t.Fatal("moving the endpoint's control plane without moving the endpoint is refused")
	}
	if err := SetNodeNetwork(dir, hash, "cp-01", nn, "https://192.168.5.51:6443"); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := c.Spec.Nodes[0]; n.TargetIP() != "192.168.5.51" || n.IP != "192.168.5.240" || c.Spec.ControlPlane.Endpoint != "https://192.168.5.51:6443" {
		t.Errorf("after the edit: target %s, ip %s, endpoint %s", n.TargetIP(), n.IP, c.Spec.ControlPlane.Endpoint)
	}
}
