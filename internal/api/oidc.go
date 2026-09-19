package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/mikael/kubit/internal/store"
)

const oidcStateCookie = "kubit_oidc"

func (s *Server) oidcRoutes() {
	s.mux.HandleFunc("GET /api/v1/auth/oidc/start", s.handleOIDCStart)
	s.mux.HandleFunc("GET /api/v1/auth/oidc/callback", s.handleOIDCCallback)
}

func (s *Server) oidcConfig(ctx context.Context, r *http.Request) (*oidc.Provider, *oauth2.Config, store.OIDC, error) {
	v, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, nil, store.OIDC{}, err
	}
	c := v.Auth.OIDC
	if !c.Enabled || c.Issuer == "" || c.ClientID == "" {
		return nil, nil, c, fmt.Errorf("single sign-on is not configured")
	}
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, nil, c, fmt.Errorf("SSO provider %s: %w", c.Issuer, err)
	}
	conf := &oauth2.Config{
		ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: externalBase(r) + "/api/v1/auth/oidc/callback",
		Scopes:      []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
	return p, conf, c, nil
}

func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	_, conf, _, err := s.oidcConfig(r.Context(), r)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	state, verifier := randomToken(), oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: state + "." + verifier, Path: "/api/v1/auth/oidc", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 600})
	http.Redirect(w, r, conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) {
		http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/v1/auth/oidc", MaxAge: -1})
		http.Redirect(w, r, "/?sso="+strings.ReplaceAll(msg, " ", "+"), http.StatusFound)
	}
	c, err := r.Cookie(oidcStateCookie)
	if err != nil {
		fail("sign-in expired, try again")
		return
	}
	state, verifier, ok := strings.Cut(c.Value, ".")
	if !ok || r.URL.Query().Get("state") != state {
		fail("sign-in state mismatch")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		fail("provider refused: " + e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p, conf, cfg, err := s.oidcConfig(ctx, r)
	if err != nil {
		fail(err.Error())
		return
	}
	tok, err := conf.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		fail("token exchange failed: " + err.Error())
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		fail("id token rejected: " + err.Error())
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		fail("claims unreadable")
		return
	}
	name := claimString(claims, cfg.UsernameClaim)
	if name == "" {
		name = claimString(claims, "email")
	}
	if name == "" {
		name = idt.Subject
	}
	role, ok := roleForGroups(cfg, claimStrings(claims, cfg.GroupsClaim))
	if !ok {
		_ = s.store.Audit(r.Context(), "", "auth.denied", name+" (no group grants access)")
		fail("your account has no role here")
		return
	}
	u, err := s.upsertSSOUser(r.Context(), name, role)
	if err != nil {
		fail(err.Error())
		return
	}
	if u.Disabled {
		fail("account disabled")
		return
	}
	sess, err := s.store.IssueToken(r.Context(), u.Name, "session", userAgent(r), sessionTTL)
	if err != nil {
		fail(err.Error())
		return
	}
	_ = s.store.Audit(store.WithActor(r.Context(), store.Actor{Name: u.Name}), "", "auth.login", u.Name+" via sso")
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/v1/auth/oidc", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds())})
	http.Redirect(w, r, "/", http.StatusFound)
}

// upsertSSOUser creates or updates the account the provider vouches for; its role
// follows the group mapping on every sign-in, so a group change takes effect at once.
func (s *Server) upsertSSOUser(ctx context.Context, name string, role store.Role) (*store.User, error) {
	name = strings.ToLower(name)
	u, err := s.store.GetUser(ctx, name)
	if err != nil {
		return s.store.CreateUser(ctx, name, "", role, "oidc")
	}
	if u.Source == "oidc" && u.Role != role {
		if err := s.store.UpdateUser(ctx, name, &role, nil, ""); err != nil {
			return nil, err
		}
		u.Role = role
	}
	return u, nil
}

func roleForGroups(c store.OIDC, groups []string) (store.Role, bool) {
	in := func(list []string) bool {
		for _, g := range groups {
			for _, want := range list {
				if strings.EqualFold(g, want) {
					return true
				}
			}
		}
		return false
	}
	switch {
	case in(c.AdminGroups):
		return store.RoleAdmin, true
	case in(c.OperatorGroups):
		return store.RoleOperator, true
	case in(c.ViewerGroups):
		return store.RoleViewer, true
	}
	if r, err := store.ParseRole(c.DefaultRole); err == nil {
		return r, true
	}
	return "", false
}

func claimString(claims map[string]any, key string) string {
	if v, ok := claims[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func claimStrings(claims map[string]any, key string) []string {
	var out []string
	switch v := claims[key].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = append(out, v)
	case json.RawMessage:
		_ = json.Unmarshal(v, &out)
	}
	return out
}
