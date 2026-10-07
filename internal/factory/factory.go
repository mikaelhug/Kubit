package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/mikaelhug/kubit/internal/httpx"
	"go.yaml.in/yaml/v4"
)

const DefaultBaseURL = "https://factory.talos.dev"

type Client struct {
	HTTP       *http.Client
	mu         sync.RWMutex
	base       string
	schematics sync.Map
}

func New() *Client { return &Client{base: DefaultBaseURL, HTTP: httpx.Client} }

func (c *Client) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base
}

func (c *Client) SetBaseURL(u string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base = u
}

type schematic struct {
	Customization struct {
		SystemExtensions struct {
			OfficialExtensions []string `yaml:"officialExtensions,omitempty"`
		} `yaml:"systemExtensions"`
	} `yaml:"customization"`
}

func (c *Client) CreateSchematic(ctx context.Context, extensions []string) (string, error) {
	var s schematic
	exts := append([]string(nil), extensions...)
	sort.Strings(exts)
	base := c.BaseURL()
	key := base + "\n" + strings.Join(exts, "\n")
	if id, ok := c.schematics.Load(key); ok {
		return id.(string), nil
	}
	s.Customization.SystemExtensions.OfficialExtensions = exts
	body, err := yaml.Marshal(s)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/schematics", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("image factory: POST /schematics: %s: %s", resp.Status, msg)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("image factory: empty schematic id")
	}
	c.schematics.Store(key, out.ID)
	return out.ID, nil
}

func (c *Client) Versions(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL()+"/versions", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image factory: GET /versions: %s", resp.Status)
	}
	var out []string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) host() string {
	return strings.TrimPrefix(c.BaseURL(), "https://")
}

func (c *Client) InstallerImage(schematicID, talosVersion string) string {
	return fmt.Sprintf("%s/metal-installer/%s:%s", c.host(), schematicID, talosVersion)
}
