package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsEditorNeverWritesInClearAndRefusesStaleOrEscapingEdits(t *testing.T) {
	s, dir := servedLab(t)
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rec := call(t, s, "POST", "/api/v1/secrets/files", `{"repo":0,"file":"apps/db.sops.yaml","name":"db","namespace":"shop","stringData":{"password":"hunter2"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "apps", "db.sops.yaml"))
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("the value must be written encrypted")
	}
	var idx struct {
		Repos []struct {
			Files []secretFileView `json:"files"`
		} `json:"repos"`
	}
	if rec := call(t, s, "GET", "/api/v1/secrets", ""); json.Unmarshal(rec.Body.Bytes(), &idx) != nil || len(idx.Repos) != 1 || len(idx.Repos[0].Files) != 1 {
		t.Fatalf("index: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "PATCH", "/api/v1/secrets/file", `{"repo":0,"file":"apps/db.sops.yaml","hash":"0000000000000000","set":[{"path":["stringData","user"],"value":"app"}]}`); rec.Code != http.StatusConflict {
		t.Errorf("stale: %d %s", rec.Code, rec.Body)
	}
	patch := `{"repo":0,"file":"apps/db.sops.yaml","hash":"` + idx.Repos[0].Files[0].Hash + `","set":[{"path":["stringData","user"],"value":"app"}]}`
	if rec := call(t, s, "PATCH", "/api/v1/secrets/file", patch); rec.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, "PATCH", "/api/v1/secrets/file", `{"repo":0,"file":"../x.sops.yaml","hash":"x","set":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("path outside the repo: %d %s", rec.Code, rec.Body)
	}
}
