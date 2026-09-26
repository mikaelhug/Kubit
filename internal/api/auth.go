package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mikael/kubit/internal/store"
)

const sessionCookie = "kubit_session"

const sessionTTL = 30 * 24 * time.Hour

func (s *Server) authRoutes() {
	r := s.mux
	r.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	r.HandleFunc("POST /api/v1/auth/setup", s.handleAuthSetup)
	r.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	r.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	r.HandleFunc("GET /api/v1/users", s.handleUsers)
	r.HandleFunc("POST /api/v1/users", s.handleUserCreate)
	r.HandleFunc("PUT /api/v1/users/{name}", s.handleUserUpdate)
	r.HandleFunc("DELETE /api/v1/users/{name}", s.handleUserDelete)
	r.HandleFunc("GET /api/v1/users/{name}/tokens", s.handleTokens)
	r.HandleFunc("POST /api/v1/users/{name}/tokens", s.handleTokenCreate)
	r.HandleFunc("DELETE /api/v1/users/{name}/tokens/{token}", s.handleTokenDelete)
	s.oidcRoutes()
}

// authenticate resolves who is calling. Order: the daemon's own bearer token (an
// automation/backwards-compatible admin), then a session cookie or a user token.
// With no users defined at all, a loopback caller is the implicit administrator so a
// fresh install works before anyone exists; anything else gets nothing.
func (s *Server) authenticate(r *http.Request) (store.Actor, bool) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if bearer == r.Header.Get("Authorization") {
		bearer = ""
	}
	if bearer == "" {
		bearer = r.URL.Query().Get("token")
	}
	if s.token != "" && bearer != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(s.token)) == 1 {
		return store.Actor{Name: "token", Role: store.RoleAdmin, Via: "token"}, true
	}
	if bearer == "" {
		if c, err := r.Cookie(sessionCookie); err == nil {
			bearer = c.Value
		}
	}
	if bearer != "" {
		if u, kind, err := s.store.ResolveToken(r.Context(), bearer); err == nil {
			return store.Actor{Name: u.Name, Role: u.Role, Via: kind}, true
		}
	}
	if n, err := s.store.CountUsers(r.Context()); err == nil && n == 0 && s.token == "" && loopbackPeer(r) {
		return store.Actor{Name: "local", Role: store.RoleAdmin, Via: "loopback"}, true
	}
	return store.Actor{}, false
}

func loopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requiredRole is the least role a request needs. Reads are for viewers, except
// credentials and exports; changes are for operators; identity, settings and
// Kubit's own backup are for administrators.
func requiredRole(r *http.Request) store.Role {
	p := r.URL.Path
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case strings.HasPrefix(p, "/api/v1/users"), p == "/api/v1/settings" && !read, strings.HasPrefix(p, "/api/v1/backup"), strings.HasPrefix(p, "/api/v1/restore"), strings.HasPrefix(p, "/api/v1/key"):
		return store.RoleAdmin
	case strings.HasSuffix(p, "/kubeconfig"), strings.HasSuffix(p, "/talosconfig"), strings.HasSuffix(p, "/export"), strings.Contains(p, "/certificates"), strings.HasSuffix(p, "/sops/identity"):
		return store.RoleAdmin
	case read:
		return store.RoleViewer
	}
	return store.RoleOperator
}

func openPath(p string) bool {
	return p == "/api/v1/version" || strings.HasPrefix(p, "/api/v1/auth/") || strings.HasPrefix(p, "/api/v1/labhost/") || p == "/api/v1/pxe/decide"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		s.mux.ServeHTTP(w, r)
		return
	}
	actor, ok := s.authenticate(r)
	if ok {
		r = r.WithContext(store.WithActor(r.Context(), actor))
	}
	if openPath(r.URL.Path) {
		s.mux.ServeHTTP(w, r)
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue", "code": "unauthorized"})
		return
	}
	if need := requiredRole(r); !actor.Role.AtLeast(need) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "this needs the " + string(need) + " role; you are " + string(actor.Role), "code": "forbidden"})
		return
	}
	s.mux.ServeHTTP(w, r)
}

type meView struct {
	User  string     `json:"user"`
	Role  store.Role `json:"role"`
	Via   string     `json:"via"`
	Setup bool       `json:"setup"`
	Users int        `json:"users"`
	SSO   string     `json:"sso,omitempty"`
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	n, _ := s.store.CountUsers(r.Context())
	sso := ""
	if v, err := s.store.GetSettings(r.Context()); err == nil && v.Auth.OIDC.Enabled && v.Auth.OIDC.Issuer != "" {
		sso = v.Auth.OIDC.Name
	}
	a, ok := store.ActorOf(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, meView{Setup: n == 0, Users: n, SSO: sso})
		return
	}
	writeJSON(w, http.StatusOK, meView{User: a.Name, Role: a.Role, Via: a.Via, Setup: n == 0, Users: n, SSO: sso})
}

type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	tok, err := s.store.IssueToken(r.Context(), u.Name, "session", userAgent(r), sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: int(sessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, meView{User: u.Name, Role: u.Role, Via: "session"})
	return nil
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 80 {
		ua = ua[:80]
	}
	return ua
}

// handleAuthSetup creates the first administrator; only possible while no user exists.
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if n > 0 {
		http.Error(w, "users exist; an administrator adds accounts under Settings", http.StatusConflict)
		return
	}
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), c.Name, c.Password, store.RoleAdmin, "local")
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(store.WithActor(r.Context(), store.Actor{Name: u.Name}), "", "user.setup", u.Name)
	if err := s.startSession(w, r, u); err != nil {
		writeErr(w, err)
	}
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, err)
		return
	}
	u, err := s.store.Authenticate(r.Context(), c.Name, c.Password)
	if err != nil {
		_ = s.store.Audit(r.Context(), "", "auth.failed", strings.ToLower(c.Name))
		if errors.Is(err, store.ErrBadCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	_ = s.store.Audit(store.WithActor(r.Context(), store.Actor{Name: u.Name}), "", "auth.login", u.Name)
	if err := s.startSession(w, r, u); err != nil {
		writeErr(w, err)
	}
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.RevokeToken(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type userRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Disabled *bool  `json:"disabled"`
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	role, err := store.ParseRole(req.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is required"})
		return
	}
	u, err := s.store.CreateUser(r.Context(), req.Name, req.Password, role, "local")
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "user.create", u.Name+" "+string(u.Role))
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	var req userRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	var role *store.Role
	if req.Role != "" {
		rl, err := store.ParseRole(req.Role)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		role = &rl
	}
	if err := s.guardLastAdmin(r.Context(), name, role, req.Disabled); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if _, err := s.store.GetUser(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.UpdateUser(r.Context(), name, role, req.Disabled, req.Password); err != nil {
		writeErr(w, err)
		return
	}
	detail := name
	if role != nil {
		detail += " role=" + string(*role)
	}
	if req.Disabled != nil {
		detail += " disabled=" + boolStr(*req.Disabled)
	}
	if req.Password != "" {
		detail += " password"
	}
	_ = s.store.Audit(r.Context(), "", "user.update", detail)
	u, _ := s.store.GetUser(r.Context(), name)
	writeJSON(w, http.StatusOK, u)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// guardLastAdmin refuses to demote, disable or delete the only enabled administrator.
func (s *Server) guardLastAdmin(ctx context.Context, name string, role *store.Role, disabled *bool) error {
	losesAdmin := (role != nil && *role != store.RoleAdmin) || (disabled != nil && *disabled) || (role == nil && disabled == nil)
	if !losesAdmin {
		return nil
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	admins := 0
	isAdmin := false
	for _, u := range users {
		if u.Role == store.RoleAdmin && !u.Disabled {
			admins++
			if u.Name == name {
				isAdmin = true
			}
		}
	}
	if isAdmin && admins == 1 {
		return errors.New("this is the only administrator; add another before changing it")
	}
	return nil
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	if err := s.guardLastAdmin(r.Context(), name, nil, nil); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.DeleteUser(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "user.delete", name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.ListTokens(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toks)
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	var req struct {
		Name string `json:"name"`
		Days int    `json:"days"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if _, err := s.store.GetUser(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}
	tok, err := s.store.IssueToken(r.Context(), name, "api", req.Name, time.Duration(req.Days)*24*time.Hour)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "token.create", name+" "+req.Name)
	writeJSON(w, http.StatusCreated, map[string]string{"token": tok})
}

func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	if err := s.store.DeleteAPIToken(r.Context(), name, r.PathValue("token")); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.Audit(r.Context(), "", "token.delete", name+" "+r.PathValue("token"))
	w.WriteHeader(http.StatusNoContent)
}
