package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/mikael/kubit/internal/offsite"
	"github.com/mikael/kubit/internal/oob"
)

// Settings are Kubit's own preferences; everything has a default so a missing row is fine.
type Settings struct {
	FactoryURL       string   `json:"factoryUrl"`
	DiscoverySubnets []string `json:"discoverySubnets"`
	WatchIntervalSec int      `json:"watchIntervalSec"`
	DefaultMetalLB   string   `json:"defaultMetalLBRange"`
	Alerts           Alerts   `json:"alerts"`
	// Offsite is the second home for snapshots and Kubit backups.
	Offsite offsite.Target `json:"offsite"`
	// AMT holds default management credentials: discovery uses them to identify
	// machines that answer on 16992, so vPro boxes show up with model and power
	// state before Talos ever ran. Password sealed at rest.
	AMT oob.Config `json:"amt"`
}

// Alerts forwards health events at or above MinSeverity to external sinks.
type Alerts struct {
	MinSeverity string `json:"minSeverity"` // warn | critical
	WebhookURL  string `json:"webhookUrl"`  // generic JSON POST; empty = off
	SMTP        SMTP   `json:"smtp"`
	// IgnoreNamespaces never raise workload alerts (noisy dev namespaces, CI).
	IgnoreNamespaces []string `json:"ignoreNamespaces"`
	// HeartbeatHours sends a "still watching" summary to the sinks this often; 0 = off.
	// It is the dead-man's switch: a missing heartbeat means the daemon is down.
	HeartbeatHours int `json:"heartbeatHours"`
}

type SMTP struct {
	Host     string   `json:"host"` // empty = off
	Port     int      `json:"port"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	Username string   `json:"username"`
	Password string   `json:"password"` // sealed at rest, never returned to the UI
	StartTLS bool     `json:"startTLS"`
	// TLS: "starttls" (587, default), "tls" (implicit, 465) or "none"; empty falls
	// back to StartTLS for settings saved before this field existed.
	TLS string `json:"tls"`
}

// Mode resolves the TLS mode including the legacy StartTLS flag.
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

func DefaultSettings() Settings {
	return Settings{FactoryURL: "https://factory.talos.dev", DiscoverySubnets: []string{}, WatchIntervalSec: 15, Alerts: Alerts{MinSeverity: "warn", SMTP: SMTP{Port: 587, StartTLS: true, TLS: "starttls", To: []string{}}, IgnoreNamespaces: []string{}, HeartbeatHours: 24}, Offsite: offsite.Target{KeepBackups: 14}, AMT: oob.Config{Type: "amt", User: "admin"}}
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
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
	out.Alerts.SMTP.Password = s.unseal(out.Alerts.SMTP.Password)
	out.Offsite.SecretKey = s.unseal(out.Offsite.SecretKey)
	out.AMT.Password = s.unseal(out.AMT.Password)
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

// GetValue and SetValue keep small daemon bookkeeping (last heartbeat, last off-site
// backup) beside the settings row.
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
	if v.Alerts.SMTP.Password, err = s.seal(v.Alerts.SMTP.Password); err != nil {
		return err
	}
	if v.Offsite.SecretKey, err = s.seal(v.Offsite.SecretKey); err != nil {
		return err
	}
	if v.AMT.Password, err = s.seal(v.AMT.Password); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('kubit', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return s.done(err, Change{Table: "settings", Key: "kubit", Op: "put"})
}
