package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/mikael/kubit/internal/store"
)

type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	groups []string
	user   string
}

func newIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, user: "alice", groups: []string{"kubit-ops"}}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	iss := f.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": iss + "/auth", "token_endpoint": iss + "/token", "jwks_uri": iss + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		u, _ := url.Parse(q.Get("redirect_uri"))
		v := u.Query()
		v.Set("code", "c0de")
		v.Set("state", q.Get("state"))
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "c0de" || r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		claims := map[string]any{"iss": iss, "sub": "sub-1", "aud": "kubit", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "preferred_username": f.user, "groups": f.groups}
		raw, _ := jwt.Signed(signer).Claims(claims).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) authorize(t *testing.T, authURL *url.URL) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL.String())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || back.RawQuery == "" {
		t.Fatalf("idp redirect: %q", resp.Header.Get("Location"))
	}
	return back.RawQuery
}

func TestOIDCSignIn(t *testing.T) {
	idp := newIdP(t)
	srv, s := newServer(t, "")
	ctx := t.Context()
	if _, err := s.CreateUser(ctx, "root", "root-password", store.RoleAdmin, "local"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetSettings(ctx)
	v.Auth.OIDC = store.OIDC{Enabled: true, Name: "Corp SSO", Issuer: idp.srv.URL, ClientID: "kubit", ClientSecret: "s3", UsernameClaim: "preferred_username", GroupsClaim: "groups", AdminGroups: []string{"kubit-admins"}, OperatorGroups: []string{"kubit-ops"}}
	if err := s.PutSettings(ctx, v); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, "GET", "/api/v1/auth/me", "")
	if !strings.Contains(rec.Body.String(), `"sso":"Corp SSO"`) {
		t.Errorf("me must advertise SSO: %s", rec.Body.String())
	}
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/start", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	stateCookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == "kubit_oidc" {
			stateCookie = c.Name + "=" + c.Value
		}
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), idp.srv.URL+"/auth") || loc.Query().Get("redirect_uri") != "http://example.com/api/v1/auth/oidc/callback" || loc.Query().Get("code_challenge") == "" {
		t.Fatalf("authorize url: %s", loc)
	}
	callback := "/api/v1/auth/oidc/callback?" + idp.authorize(t, loc)
	rec = do(t, srv, "GET", callback, "", "Cookie", stateCookie)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback: %d %s → %s", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	session := cookieOf(rec)
	if session == "" {
		t.Fatal("no session after SSO")
	}
	rec = do(t, srv, "GET", "/api/v1/auth/me", "", "Cookie", session)
	if !strings.Contains(rec.Body.String(), `"user":"alice"`) || !strings.Contains(rec.Body.String(), `"role":"operator"`) {
		t.Errorf("sso identity: %s", rec.Body.String())
	}
	u, _ := s.GetUser(ctx, "alice")
	if u == nil || u.Source != "oidc" || u.HasPass {
		t.Errorf("sso user row: %+v", u)
	}
	if _, err := s.Authenticate(ctx, "alice", "anything"); err == nil {
		t.Error("an SSO account has no password to sign in with")
	}
	idp.groups = []string{"kubit-admins"}
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/start", "")
	for _, c := range rec.Result().Cookies() {
		if c.Name == "kubit_oidc" {
			stateCookie = c.Name + "=" + c.Value
		}
	}
	loc, _ = url.Parse(rec.Header().Get("Location"))
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/callback?"+idp.authorize(t, loc), "", "Cookie", stateCookie)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("second callback: %d → %s", rec.Code, rec.Header().Get("Location"))
	}
	if u, _ := s.GetUser(ctx, "alice"); u.Role != store.RoleAdmin {
		t.Errorf("role follows the group mapping on each sign-in: %s", u.Role)
	}
	idp.groups, idp.user = []string{"unrelated"}, "bob"
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/start", "")
	for _, c := range rec.Result().Cookies() {
		if c.Name == "kubit_oidc" {
			stateCookie = c.Name + "=" + c.Value
		}
	}
	loc, _ = url.Parse(rec.Header().Get("Location"))
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/callback?"+idp.authorize(t, loc), "", "Cookie", stateCookie)
	if !strings.Contains(rec.Header().Get("Location"), "no+role") {
		t.Errorf("no matching group and no default role must be refused: %s", rec.Header().Get("Location"))
	}
	if _, err := s.GetUser(ctx, "bob"); err == nil {
		t.Error("a refused sign-in must not create an account")
	}
	rec = do(t, srv, "GET", "/api/v1/auth/oidc/callback?code=x&state=y", "", "Cookie", "kubit_oidc=z.v")
	if !strings.Contains(rec.Header().Get("Location"), "mismatch") {
		t.Errorf("state mismatch: %s", rec.Header().Get("Location"))
	}
}
