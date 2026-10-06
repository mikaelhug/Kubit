package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestClusterYAMLRefusesAStaleEdit(t *testing.T) {
	s, dir := servedLab(t)
	var got struct{ YAML, Hash string }
	if rec := call(t, s, http.MethodGet, "/api/v1/clusters/lab/yaml", ""); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Hash == "" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	body := func(yaml, hash string) string {
		b, _ := json.Marshal(map[string]string{"yaml": yaml, "hash": hash})
		return string(b)
	}
	edited := "# the lab under the desk\n" + got.YAML
	if rec := call(t, s, http.MethodPut, "/api/v1/clusters/lab/yaml", body(edited, "0000000000000000")); rec.Code != http.StatusConflict {
		t.Errorf("stale: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, http.MethodPut, "/api/v1/clusters/lab/yaml", body(edited, got.Hash)); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cluster.yaml")); string(b) != edited {
		t.Errorf("the file is written as typed, comments included:\n%s", b)
	}
}
