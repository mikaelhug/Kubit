package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/store"
	"github.com/mikaelhug/kubit/internal/talos"
)

func maintenanceMachine(t *testing.T, st *store.Store, mac, ip string) {
	t.Helper()
	inv := talos.Inventory{IP: ip, Arch: "amd64", CPUs: 4, MemoryBytes: 8 << 30, TalosVersion: "v1.14.2",
		Links: []talos.Link{{Name: "eth0", MAC: mac, Up: true}},
		Disks: []talos.Disk{{DevPath: "/dev/nvme0n1", SizeBytes: 256 << 30}}}
	hw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	st.UpsertNode(store.Machine{MAC: mac, IP: ip, State: "maintenance", Hardware: hw})
}

func TestCreateRepoNeverOverwritesAnExistingRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	id, _ := age.GenerateX25519Identity()
	t.Setenv("SOPS_AGE_KEY", id.String())
	s, st := localServer(t)
	maintenanceMachine(t, st, "98:e7:f4:be:4d:9e", "127.0.0.1")
	maintenanceMachine(t, st, "98:e7:f4:be:4d:9f", "127.0.0.2")
	if rec := call(t, s, http.MethodPost, "/api/v1/repos", `{"dir":"~/git/home","machines":[{"mac":"98:e7:f4:be:4d:9e"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	file := filepath.Join(home, "git", "home", "cluster.yaml")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, http.MethodPost, "/api/v1/repos", `{"dir":"~/git/home","machines":[{"mac":"98:e7:f4:be:4d:9f"}]}`); rec.Code != http.StatusConflict {
		t.Errorf("second create: %d %s", rec.Code, rec.Body)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Errorf("cluster.yaml changed:\n%s", after)
	}
}
