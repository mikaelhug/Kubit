package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/repo"
)

func TestSecretsEditorRoundTrip(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOPS_AGE_KEY", id.String())
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := config.Parse([]byte("apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n    - {hostname: cp-01, ip: 127.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "lab")
	if _, err := repo.Init(dir, c, []string{id.Recipient().String()}); err != nil {
		t.Fatal(err)
	}
	s, _ := localServer(t)
	if err := s.ServeRepos(t.Context(), []string{dir}); err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, "POST", "/api/v1/secrets/files", `{"repo":0,"file":"apps/db.sops.yaml","name":"db","namespace":"shop"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "PUT", "/api/v1/secrets/value", `{"repo":0,"file":"apps/db.sops.yaml","key":["stringData","password"],"value":"hunter2"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "apps", "db.sops.yaml"))
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("the value must be written encrypted")
	}
	var listed []struct {
		Cluster string            `json:"cluster"`
		Files   []repo.SecretFile `json:"files"`
	}
	rec := call(t, s, "GET", "/api/v1/secrets", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 1 || len(listed[0].Files) != 1 {
		t.Fatalf("list: %s", rec.Body)
	}
	if f := listed[0].Files[0]; f.Name != "db" || len(f.Keys) == 0 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("listed %+v", f)
	}
	q := url.Values{"repo": {"0"}, "file": {"apps/db.sops.yaml"}, "key": {"stringData", "password"}}
	rec = call(t, s, "GET", "/api/v1/secrets/value?"+q.Encode(), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hunter2"`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "DELETE", "/api/v1/secrets/value?"+q.Encode(), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "GET", "/api/v1/secrets/value?"+q.Encode(), ""); rec.Code != http.StatusConflict {
		t.Errorf("a deleted value: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{`{"repo":0,"file":"../x.sops.yaml","key":["a"],"value":"b"}`, `{"repo":0,"file":"secrets.sops.yaml","key":["a"],"value":"b"}`, `{"repo":3,"file":"apps/db.sops.yaml","key":["a"],"value":"b"}`, `{"repo":0,"file":"apps/db.sops.yaml","key":[],"value":"b"}`} {
		if rec := call(t, s, "PUT", "/api/v1/secrets/value", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
}
