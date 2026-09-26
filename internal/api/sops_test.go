package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"
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
	rec = do(t, srv, "GET", "/api/v1/clusters/lab/sops/identity", "")
	ids, err := age.ParseIdentities(rec.Body)
	if rec.Code != http.StatusOK || err != nil || len(ids) != 1 || ids[0].(*age.X25519Identity).Recipient().String() != k.Recipient {
		t.Fatalf("export: %d %v", rec.Code, err)
	}
	if rec := do(t, srv, "PUT", "/api/v1/clusters/lab/sops/identity", keysBody("not a key")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("garbage import: %d %s", rec.Code, rec.Body.String())
	}
	id, _ := age.GenerateX25519Identity()
	rec = do(t, srv, "PUT", "/api/v1/clusters/lab/sops/identity", keysBody("# mine\n"+id.String()+"\n"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id.Recipient().String()) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	two, _ := age.GenerateX25519Identity()
	if rec := do(t, srv, "PUT", "/api/v1/clusters/lab/sops/identity", keysBody(id.String()+"\n"+two.String()+"\n")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("two keys: %d", rec.Code)
	}

	admin := cookieOf(do(t, srv, "POST", "/api/v1/auth/setup", `{"name":"admin","password":"correct horse"}`))
	do(t, srv, "POST", "/api/v1/users", `{"name":"ro","password":"viewer-pass1","role":"viewer"}`, "Cookie", admin)
	ro := cookieOf(do(t, srv, "POST", "/api/v1/auth/login", `{"name":"ro","password":"viewer-pass1"}`))
	if rec := do(t, srv, "GET", "/api/v1/clusters/lab/sops", "", "Cookie", ro); rec.Code != http.StatusOK {
		t.Errorf("viewer reads the recipient: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters/lab/sops/identity", "", "Cookie", ro); rec.Code != http.StatusForbidden {
		t.Errorf("viewer must not export the key: %d", rec.Code)
	}
	if rec := do(t, srv, "PUT", "/api/v1/clusters/lab/sops/identity", keysBody(id.String()), "Cookie", ro); rec.Code != http.StatusForbidden {
		t.Errorf("viewer must not import a key: %d", rec.Code)
	}
	rec = do(t, srv, "GET", "/api/v1/audit", "", "Cookie", admin)
	if !strings.Contains(rec.Body.String(), `"sops.export"`) || !strings.Contains(rec.Body.String(), `"sops.import"`) {
		t.Errorf("export and import are audited: %s", rec.Body.String())
	}
}

func keysBody(keys string) string {
	b, _ := json.Marshal(map[string]string{"keys": keys})
	return string(b)
}
