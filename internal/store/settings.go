package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/mikael/kubit/internal/offsite"
	"github.com/mikael/kubit/internal/oob"
)

type Settings struct {
	FactoryURL       string         `json:"factoryUrl"`
	DiscoverySubnets []string       `json:"discoverySubnets"`
	WatchIntervalSec int            `json:"watchIntervalSec"`
	PXEStatusURL     string         `json:"pxeStatusUrl"`
	DefaultMetalLB   string         `json:"defaultMetalLBRange"`
	Alerts           Alerts         `json:"alerts"`
	Offsite          offsite.Target `json:"offsite"`
	PXEEnrollment    string         `json:"pxeEnrollment"`
	AMT              oob.Config     `json:"amt"`
	BMC              oob.Config     `json:"bmc"`
	Auth             Auth           `json:"auth"`
}

type Auth struct {
	OIDC OIDC `json:"oidc"`
}

type OIDC struct {
	Enabled        bool     `json:"enabled"`
	Name           string   `json:"name"`
	Issuer         string   `json:"issuer"`
	ClientID       string   `json:"clientId"`
	ClientSecret   string   `json:"clientSecret"`
	UsernameClaim  string   `json:"usernameClaim"`
	GroupsClaim    string   `json:"groupsClaim"`
	AdminGroups    []string `json:"adminGroups"`
	OperatorGroups []string `json:"operatorGroups"`
	ViewerGroups   []string `json:"viewerGroups"`
	DefaultRole    string   `json:"defaultRole"`
}

type Alerts struct {
	MinSeverity      string   `json:"minSeverity"`
	WebhookURL       string   `json:"webhookUrl"`
	SMTP             SMTP     `json:"smtp"`
	IgnoreNamespaces []string `json:"ignoreNamespaces"`
	HeartbeatHours   int      `json:"heartbeatHours"`
}

type SMTP struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	StartTLS bool     `json:"startTLS"`
	TLS      string   `json:"tls"`
}

func (c SMTP) Mode() string {
	switch c.TLS {
	case "tls", "none", "starttls":
		return c.TLS
	}
	if c.StartTLS {
		return "starttls"
	}
	return "none"
}

const Masked = "•••"

func (v *Settings) Secrets() []*string {
	return []*string{&v.Alerts.SMTP.Password, &v.Offsite.SecretKey, &v.AMT.Password, &v.BMC.Password, &v.Auth.OIDC.ClientSecret}
}

func DefaultSettings() Settings {
	return Settings{FactoryURL: "https://factory.talos.dev", DiscoverySubnets: []string{}, WatchIntervalSec: 15, PXEStatusURL: "http://127.0.0.1:8069/status.json", Alerts: Alerts{MinSeverity: "warn", SMTP: SMTP{Port: 587, StartTLS: true, TLS: "starttls", To: []string{}}, IgnoreNamespaces: []string{}, HeartbeatHours: 24}, Offsite: offsite.Target{KeepBackups: 14}, PXEEnrollment: "open", AMT: oob.Config{Type: "amt", User: "admin"}, BMC: oob.Config{Type: "redfish", User: "root"}, Auth: Auth{OIDC: OIDC{Name: "SSO", UsernameClaim: "preferred_username", GroupsClaim: "groups", AdminGroups: []string{}, OperatorGroups: []string{}, ViewerGroups: []string{}}}}
}

func (v Settings) clone() Settings {
	v.DiscoverySubnets = slices.Clone(v.DiscoverySubnets)
	v.Alerts.SMTP.To = slices.Clone(v.Alerts.SMTP.To)
	v.Alerts.IgnoreNamespaces = slices.Clone(v.Alerts.IgnoreNamespaces)
	v.Auth.OIDC.AdminGroups = slices.Clone(v.Auth.OIDC.AdminGroups)
	v.Auth.OIDC.OperatorGroups = slices.Clone(v.Auth.OIDC.OperatorGroups)
	v.Auth.OIDC.ViewerGroups = slices.Clone(v.Auth.OIDC.ViewerGroups)
	return v
}

type settingsCache struct {
	mu    sync.Mutex
	gen   uint64
	value *Settings
}

func (c *settingsCache) invalidate() {
	c.mu.Lock()
	c.gen++
	c.value = nil
	c.mu.Unlock()
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	s.settings.mu.Lock()
	cached, gen := s.settings.value, s.settings.gen
	s.settings.mu.Unlock()
	if cached != nil {
		return cached.clone(), nil
	}
	out, err := s.readSettings(ctx)
	if err != nil {
		return out, err
	}
	s.settings.mu.Lock()
	if s.settings.gen == gen {
		v := out.clone()
		s.settings.value = &v
	}
	s.settings.mu.Unlock()
	return out, nil
}

func (s *Store) readSettings(ctx context.Context) (Settings, error) {
	out := DefaultSettings()
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'kubit'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	for _, p := range out.Secrets() {
		*p = s.unseal(*p)
	}
	if out.BMC.Type == "" {
		out.BMC.Type, out.BMC.User = "redfish", "root"
	}
	if out.Auth.OIDC.UsernameClaim == "" {
		out.Auth.OIDC.UsernameClaim = "preferred_username"
	}
	if out.Auth.OIDC.GroupsClaim == "" {
		out.Auth.OIDC.GroupsClaim = "groups"
	}
	if out.Auth.OIDC.Name == "" {
		out.Auth.OIDC.Name = "SSO"
	}
	for _, g := range []*[]string{&out.Auth.OIDC.AdminGroups, &out.Auth.OIDC.OperatorGroups, &out.Auth.OIDC.ViewerGroups} {
		if *g == nil {
			*g = []string{}
		}
	}
	return out, nil
}

func (s *Store) unseal(v string) string {
	if v == "" {
		return ""
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		if plain, err := s.crypto.Open(b); err == nil {
			return string(plain)
		}
	}
	return v
}

func (s *Store) seal(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	sealed, err := s.crypto.Seal([]byte(v))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Store) GetValue(ctx context.Context, key string) string {
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) SetValue(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return s.done(err, Change{Table: "settings", Key: key, Op: "put"})
}

func (s *Store) PutSettings(ctx context.Context, v Settings) error {
	var err error
	for _, p := range v.Secrets() {
		if *p, err = s.seal(*p); err != nil {
			return err
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('kubit', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return s.done(err, Change{Table: "settings", Key: "kubit", Op: "put"})
}
