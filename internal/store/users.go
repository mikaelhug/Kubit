package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleViewer, RoleOperator, RoleAdmin:
		return Role(s), nil
	}
	return "", fmt.Errorf("role must be viewer, operator or admin")
}

func (r Role) AtLeast(min Role) bool { return rank(r) >= rank(min) }

func rank(r Role) int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

type User struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      Role   `json:"role"`
	Disabled  bool   `json:"disabled"`
	Source    string `json:"source"`
	CreatedAt string `json:"createdAt"`
	LastLogin string `json:"lastLogin,omitempty"`
	HasPass   bool   `json:"hasPassword"`
}

type Token struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	LastUsed  string `json:"lastUsed,omitempty"`
	Prefix    string `json:"prefix"`
}

type Actor struct {
	Name string
	Role Role
	Via  string
}

type actorKey struct{}

func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func ActorOf(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

func ActorFrom(ctx context.Context) string {
	if a, ok := ActorOf(ctx); ok {
		return a.Name
	}
	return ""
}

var ErrBadCredentials = errors.New("wrong user or password")

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kubit-timing-pad"), bcrypt.DefaultCost)

const userCols = `id, name, role, disabled, source, created_at, last_login, password_hash != ''`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var u User
	var disabled int
	if err := sc.Scan(&u.ID, &u.Name, &u.Role, &disabled, &u.Source, &u.CreatedAt, &u.LastLogin, &u.HasPass); err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, name string) (*User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE name = ?`, strings.ToLower(name)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %s: %w", name, ErrNotFound)
	}
	return u, err
}

func validName(name string) error {
	if len(name) < 2 || len(name) > 64 || strings.ContainsAny(name, " \t\n/\\") {
		return fmt.Errorf("name must be 2-64 characters without spaces or slashes")
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, name, password string, role Role, source string) (*User, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := validName(name); err != nil {
		return nil, err
	}
	hash := ""
	if password != "" {
		if len(password) < 8 {
			return nil, fmt.Errorf("password must be at least 8 characters")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		hash = string(h)
	}
	if source == "" {
		source = "local"
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (name, password_hash, role, source) VALUES (?, ?, ?, ?)`, name, hash, role, source); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("user %s exists", name)
		}
		return nil, err
	}
	s.notify(Change{Table: "users", Key: name, Op: "put"})
	return s.GetUser(ctx, name)
}

func (s *Store) UpdateUser(ctx context.Context, name string, role *Role, disabled *bool, password string) error {
	name = strings.ToLower(name)
	if role != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE name = ?`, *role, name); err != nil {
			return err
		}
	}
	if disabled != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE name = ?`, boolInt(*disabled), name); err != nil {
			return err
		}
		if *disabled {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE kind = 'session' AND user_id = (SELECT id FROM users WHERE name = ?)`, name); err != nil {
				return err
			}
		}
	}
	if password != "" {
		if len(password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE name = ?`, string(h), name); err != nil {
			return err
		}
	}
	s.notify(Change{Table: "users", Key: name, Op: "put"})
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE name = ?`, strings.ToLower(name))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %s: %w", name, ErrNotFound)
	}
	s.notify(Change{Table: "users", Key: strings.ToLower(name), Op: "delete"})
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) Authenticate(ctx context.Context, name, password string) (*User, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	var hash string
	var disabled int
	err := s.db.QueryRowContext(ctx, `SELECT password_hash, disabled FROM users WHERE name = ?`, name).Scan(&hash, &disabled)
	if errors.Is(err, sql.ErrNoRows) || hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrBadCredentials
	}
	if disabled != 0 {
		return nil, fmt.Errorf("user %s is disabled", name)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_login = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE name = ?`, name)
	return s.GetUser(ctx, name)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (s *Store) IssueToken(ctx context.Context, user string, kind, name string, ttl time.Duration) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	prefix := "kbs_"
	if kind == "api" {
		prefix = "kbt_"
	}
	token := prefix + hex.EncodeToString(b)
	expires := ""
	if ttl > 0 {
		expires = time.Now().Add(ttl).UTC().Format(time.RFC3339)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, kind, name, expires_at) SELECT ?, id, ?, ?, ? FROM users WHERE name = ?`, hashToken(token), kind, name, expires, strings.ToLower(user))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("user %s: %w", user, ErrNotFound)
	}
	return token, nil
}

func (s *Store) ResolveToken(ctx context.Context, token string) (*User, string, error) {
	var kind, expires string
	var user User
	var disabled int
	err := s.db.QueryRowContext(ctx, `SELECT s.kind, s.expires_at, u.id, u.name, u.role, u.disabled, u.source, u.created_at, u.last_login
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, hashToken(token)).Scan(&kind, &expires, &user.ID, &user.Name, &user.Role, &disabled, &user.Source, &user.CreatedAt, &user.LastLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if disabled != 0 {
		return nil, "", fmt.Errorf("user %s is disabled", user.Name)
	}
	if expires != "" {
		if t, err := time.Parse(time.RFC3339, expires); err == nil && time.Now().After(t) {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
			return nil, "", ErrNotFound
		}
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_used = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE token_hash = ?`, hashToken(token))
	return &user, kind, nil
}

func (s *Store) RevokeToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func (s *Store) ListTokens(ctx context.Context, user string) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.name, s.kind, s.created_at, s.expires_at, s.last_used, substr(s.token_hash, 1, 8)
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.name = ? AND s.kind = 'api' ORDER BY s.created_at`, strings.ToLower(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.Name, &t.Kind, &t.CreatedAt, &t.ExpiresAt, &t.LastUsed, &t.Prefix); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIToken(ctx context.Context, user, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE kind = 'api' AND name = ? AND user_id = (SELECT id FROM users WHERE name = ?)`, name, strings.ToLower(user))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("token %s: %w", name, ErrNotFound)
	}
	return nil
}
