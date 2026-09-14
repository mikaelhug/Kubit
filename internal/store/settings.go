package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Settings are Kubit's own preferences; everything has a default so a missing row is fine.
type Settings struct {
	FactoryURL       string   `json:"factoryUrl"`
	DiscoverySubnets []string `json:"discoverySubnets"`
	WatchIntervalSec int      `json:"watchIntervalSec"`
	PXEStatusURL     string   `json:"pxeStatusUrl"`
	DefaultMetalLB   string   `json:"defaultMetalLBRange"`
}

func DefaultSettings() Settings {
	return Settings{FactoryURL: "https://factory.talos.dev", DiscoverySubnets: []string{}, WatchIntervalSec: 15, PXEStatusURL: "http://127.0.0.1:8069/status.json"}
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
	return out, nil
}

func (s *Store) PutSettings(ctx context.Context, v Settings) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('kubit', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}
