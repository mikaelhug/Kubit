package tofu

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

type stateStamp struct {
	mod, size int64
}

type releasesEntry struct {
	stamp    stateStamp
	releases []Release
}

var (
	releasesMu    sync.Mutex
	releasesCache = map[string]releasesEntry{}
)

func (r *Runner) Releases(ctx context.Context) ([]Release, error) {
	fi, err := os.Stat(filepath.Join(r.Dir, "terraform.tfstate"))
	if err != nil {
		return []Release{}, nil
	}
	stamp := stateStamp{mod: fi.ModTime().UnixNano(), size: fi.Size()}
	releasesMu.Lock()
	e, ok := releasesCache[r.Dir]
	releasesMu.Unlock()
	if ok && e.stamp == stamp {
		return slices.Clone(e.releases), nil
	}
	out, err := r.output(ctx, "show", "-json")
	if err != nil {
		return nil, err
	}
	releases, err := ParseReleases(out)
	if err != nil {
		return nil, err
	}
	releasesMu.Lock()
	releasesCache[r.Dir] = releasesEntry{stamp: stamp, releases: releases}
	releasesMu.Unlock()
	return slices.Clone(releases), nil
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
