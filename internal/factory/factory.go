// Package factory talks to the Talos Image Factory (factory.talos.dev), which turns a
// schematic (Talos version + system extensions) into installer images and boot assets.
package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"go.yaml.in/yaml/v4"
)

const DefaultBaseURL = "https://factory.talos.dev"

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New() *Client { return &Client{BaseURL: DefaultBaseURL, HTTP: http.DefaultClient} }

type schematic struct {
	Customization struct {
		SystemExtensions struct {
			OfficialExtensions []string `yaml:"officialExtensions,omitempty"`
		} `yaml:"systemExtensions"`
	} `yaml:"customization"`
}

// CreateSchematic registers the extension set and returns the schematic ID. The ID is a
// content hash, so calling it repeatedly with the same extensions is idempotent.
func (c *Client) CreateSchematic(ctx context.Context, extensions []string) (string, error) {
	var s schematic
	exts := append([]string(nil), extensions...)
	sort.Strings(exts)
	s.Customization.SystemExtensions.OfficialExtensions = exts
	body, err := yaml.Marshal(s)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/schematics", bytes.NewReader(body))
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
	return out.ID, nil
}

// Versions lists the Talos releases the factory can build images for.
func (c *Client) Versions(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/versions", nil)
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
	const p = "https://"
	if len(c.BaseURL) > len(p) && c.BaseURL[:len(p)] == p {
		return c.BaseURL[len(p):]
	}
	return c.BaseURL
}

// InstallerImage is the OCI reference Talos installs from (machine.install.image).
func (c *Client) InstallerImage(schematicID, talosVersion string) string {
	return fmt.Sprintf("%s/metal-installer/%s:%s", c.host(), schematicID, talosVersion)
}

func (c *Client) ISOURL(schematicID, talosVersion, arch string) string {
	return fmt.Sprintf("%s/image/%s/%s/metal-%s.iso", c.BaseURL, schematicID, talosVersion, arch)
}

func (c *Client) KernelURL(schematicID, talosVersion, arch string) string {
	return fmt.Sprintf("%s/image/%s/%s/kernel-%s", c.BaseURL, schematicID, talosVersion, arch)
}

func (c *Client) InitramfsURL(schematicID, talosVersion, arch string) string {
	return fmt.Sprintf("%s/image/%s/%s/initramfs-%s.xz", c.BaseURL, schematicID, talosVersion, arch)
}

// PXEURL returns an iPXE script that boots Talos metal for the architecture.
func (c *Client) PXEURL(schematicID, talosVersion, arch string) string {
	return fmt.Sprintf("%s/pxe/%s/%s/metal-%s", c.BaseURL, schematicID, talosVersion, arch)
}
