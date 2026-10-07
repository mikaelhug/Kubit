package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDestroyIsRefusedWithoutTheExactClusterName(t *testing.T) {
	s, _ := servedLab(t)
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"Lab"}`, `{"name":"lab "}`} {
		if rec := call(t, s, "POST", "/api/v1/clusters/lab/destroy", body); rec.Code != http.StatusConflict {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	var run applyRun
	if rec := call(t, s, "GET", "/api/v1/clusters/lab/apply", ""); json.Unmarshal(rec.Body.Bytes(), &run) != nil || run.Running || run.Kind != "" {
		t.Fatalf("a refused destroy must start nothing: %s", rec.Body)
	}
}
