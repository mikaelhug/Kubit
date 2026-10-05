package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestSOPSKeyEndpoints(t *testing.T) {
	srv, st := newServer(t, "")
	if err := st.PutCluster(context.Background(), store.ClusterRow{Name: "lab", Spec: []byte("spec: 1")}); err != nil {
		t.Fatal(err)
	}
	var k struct{ Recipient, CreatedAt string }
	rec := do(t, srv, "GET", "/api/v1/clusters/lab/sops", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &k)
	if rec.Code != http.StatusOK || !strings.HasPrefix(k.Recipient, "age1") || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("recipient: %d %s", rec.Code, rec.Body.String())
	}
	if again := do(t, srv, "GET", "/api/v1/clusters/lab/sops", ""); !strings.Contains(again.Body.String(), k.Recipient) {
		t.Errorf("the key must be stable: %s", again.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters/nope/sops", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown cluster: %d", rec.Code)
	}
}
