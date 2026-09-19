package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func cookieOf(rec interface{ Result() *http.Response }) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "kubit_session" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

func TestIdentityLifecycle(t *testing.T) {
	srv, _ := newServer(t, "")
	var me struct {
		User  string
		Role  string
		Via   string
		Setup bool
	}
	rec := do(t, srv, "GET", "/api/v1/auth/me", "")
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || me.Via != "loopback" || me.Role != "admin" || !me.Setup {
		t.Fatalf("fresh install on loopback is an implicit admin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "POST", "/api/v1/auth/setup", `{"name":"Mikael","password":"short"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("weak password: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, srv, "POST", "/api/v1/auth/setup", `{"name":"Mikael","password":"correct horse"}`)
	if rec.Code != http.StatusOK || cookieOf(rec) == "" {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	admin := cookieOf(rec)
	if rec := do(t, srv, "POST", "/api/v1/auth/setup", `{"name":"x","password":"correct horse"}`); rec.Code != http.StatusConflict {
		t.Errorf("second setup: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("once a user exists loopback is no longer implicit: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Cookie", admin); rec.Code != http.StatusOK {
		t.Errorf("session cookie: %d", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/auth/login", `{"name":"mikael","password":"wrong"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad login: %d", rec.Code)
	}
	rec = do(t, srv, "POST", "/api/v1/users", `{"name":"ops","password":"operator-pass","role":"operator"}`, "Cookie", admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create operator: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, srv, "POST", "/api/v1/users", `{"name":"ro","password":"viewer-pass1","role":"viewer"}`, "Cookie", admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", rec.Code, rec.Body.String())
	}
	login := func(name, pass string) string {
		rec := do(t, srv, "POST", "/api/v1/auth/login", `{"name":"`+name+`","password":"`+pass+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s: %d %s", name, rec.Code, rec.Body.String())
		}
		return cookieOf(rec)
	}
	ops, ro := login("ops", "operator-pass"), login("ro", "viewer-pass1")
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Cookie", ro); rec.Code != http.StatusOK {
		t.Errorf("viewer reads: %d", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/machines", `{"mac":"aa:bb:cc:dd:ee:ff"}`, "Cookie", ro); rec.Code != http.StatusForbidden {
		t.Errorf("viewer must not change anything: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters/c/kubeconfig", "", "Cookie", ro); rec.Code != http.StatusForbidden {
		t.Errorf("viewer must not read credentials: %d", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/machines", `{"mac":"aa:bb:cc:dd:ee:ff"}`, "Cookie", ops); rec.Code != http.StatusCreated {
		t.Errorf("operator changes: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/users", "", "Cookie", ops); rec.Code != http.StatusForbidden {
		t.Errorf("operator must not manage users: %d", rec.Code)
	}
	if rec := do(t, srv, "PUT", "/api/v1/users/mikael", `{"role":"viewer"}`, "Cookie", admin); rec.Code != http.StatusConflict {
		t.Errorf("demoting the only admin must be refused: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "DELETE", "/api/v1/users/mikael", "", "Cookie", admin); rec.Code != http.StatusConflict {
		t.Errorf("deleting the only admin must be refused: %d", rec.Code)
	}
	rec = do(t, srv, "POST", "/api/v1/users/ops/tokens", `{"name":"ci","days":30}`, "Cookie", admin)
	var tok struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	if rec.Code != http.StatusCreated || !strings.HasPrefix(tok.Token, "kbt_") {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, srv, "GET", "/api/v1/auth/me", "", "Authorization", "Bearer "+tok.Token)
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me.User != "ops" || me.Role != "operator" || me.Via != "api" {
		t.Errorf("api token identity: %s", rec.Body.String())
	}
	if rec := do(t, srv, "PUT", "/api/v1/users/ops", `{"disabled":true}`, "Cookie", admin); rec.Code != http.StatusOK {
		t.Errorf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Cookie", ops); rec.Code != http.StatusUnauthorized {
		t.Errorf("a disabled user's session must stop working: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Authorization", "Bearer "+tok.Token); rec.Code != http.StatusUnauthorized {
		t.Errorf("a disabled user's token must stop working: %d", rec.Code)
	}
	if rec := do(t, srv, "POST", "/api/v1/auth/logout", "", "Cookie", admin); rec.Code != http.StatusNoContent {
		t.Errorf("logout: %d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/v1/clusters", "", "Cookie", admin); rec.Code != http.StatusUnauthorized {
		t.Errorf("session revoked on logout: %d", rec.Code)
	}
	rec = do(t, srv, "GET", "/api/v1/audit", "", "Cookie", login("mikael", "correct horse"))
	if !strings.Contains(rec.Body.String(), `"actor":"mikael"`) || !strings.Contains(rec.Body.String(), `"user.create"`) {
		t.Errorf("audit must name the actor: %s", rec.Body.String())
	}
}
