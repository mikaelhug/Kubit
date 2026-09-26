package tofu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Release struct {
	Address      string `json:"address"`
	Addon        string `json:"addon"`
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	Chart        string `json:"chart"`
	ChartVersion string `json:"chartVersion"`
	AppVersion   string `json:"appVersion,omitempty"`
	Status       string `json:"status"`
	LastDeployed int64  `json:"lastDeployed,omitempty"`
}

func (r *Runner) Releases(ctx context.Context) ([]Release, error) {
	if _, err := os.Stat(filepath.Join(r.Dir, "terraform.tfstate")); err != nil {
		return []Release{}, nil
	}
	cmd := exec.CommandContext(ctx, r.Bin, "show", "-json")
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tofu show: %w", err)
	}
	return ParseReleases(out)
}

func ParseReleases(raw []byte) ([]Release, error) {
	var st struct {
		Values struct {
			RootModule struct {
				Resources []struct {
					Address string         `json:"address"`
					Type    string         `json:"type"`
					Values  map[string]any `json:"values"`
				} `json:"resources"`
			} `json:"root_module"`
		} `json:"values"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	out := []Release{}
	for _, res := range st.Values.RootModule.Resources {
		if res.Type != "helm_release" {
			continue
		}
		v := res.Values
		rel := Release{Address: res.Address, Addon: AddonOf(res.Address), Name: str(v["name"]), Namespace: str(v["namespace"]), Chart: str(v["chart"]), ChartVersion: str(v["version"]), Status: str(v["status"])}
		if md, ok := v["metadata"].(map[string]any); ok {
			rel.AppVersion = str(md["app_version"])
			if f, ok := md["last_deployed"].(float64); ok {
				rel.LastDeployed = int64(f)
			}
		}
		out = append(out, rel)
	}
	return out, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
