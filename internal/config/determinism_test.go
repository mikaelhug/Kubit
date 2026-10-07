package config_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mikaelhug/kubit/internal/config"
)

func TestGenerateIsDeterministic(t *testing.T) {
	c, err := config.Parse([]byte(sampleCluster))
	if err != nil {
		t.Fatal(err)
	}
	b := sharedSecrets(t)
	first, err := config.Generate(c, b, "installer")
	if err != nil {
		t.Fatal(err)
	}
	again, err := config.Generate(c.Clone(), b, "installer")
	if err != nil {
		t.Fatal(err)
	}
	for host, cfg := range first.Nodes {
		if !bytes.Equal(cfg, again.Nodes[host]) {
			t.Errorf("%s: the same declaration and secrets must give the same bytes", host)
		}
	}
}

func TestMinSizeAcceptsBinaryUnits(t *testing.T) {
	spec := strings.Replace(sampleCluster, "minSize: 10GB", "minSize: 500GiB", 1)
	if spec == sampleCluster {
		t.Fatal("sample has no minSize to replace")
	}
	c, err := config.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("GiB is a size: %v", err)
	}
	if _, err := config.Generate(c, sharedSecrets(t), "installer"); err != nil {
		t.Errorf("a GiB selector compiles: %v", err)
	}
}

func TestRecordingTheMovedAddressChangesNoConfig(t *testing.T) {
	moving := strings.Replace(spec(", network: { addresses: [192.168.5.51/24], gateway: 192.168.5.1 }", ""), "https://192.168.5.240:6443", "https://192.168.5.51:6443", 1)
	recorded := strings.Replace(moving, "ip: 192.168.5.240", "ip: 192.168.5.51", 1)
	a, err := config.Parse([]byte(moving))
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.Parse([]byte(recorded))
	if err != nil {
		t.Fatal(err)
	}
	s := sharedSecrets(t)
	ga, err := config.Generate(a, s, "installer")
	if err != nil {
		t.Fatal(err)
	}
	gb, err := config.Generate(b, s, "installer")
	if err != nil {
		t.Fatal(err)
	}
	for host, cfg := range ga.Nodes {
		if !bytes.Equal(cfg, gb.Nodes[host]) {
			t.Errorf("%s: writing the new address to ip: after a move must not change its config", host)
		}
	}
}
