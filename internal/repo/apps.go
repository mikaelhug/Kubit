package repo

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/gitremote"
	"github.com/mikaelhug/kubit/internal/sops"
	"github.com/mikaelhug/kubit/internal/tofu"
	"github.com/mikaelhug/kubit/internal/yamlx"
)

type AppsKind string

const (
	AppsEmpty  AppsKind = "empty"
	AppsLayout AppsKind = "layout"
	AppsOther  AppsKind = "other"
)

type AppsCluster struct {
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
}

type AppsRepo struct {
	Dir          string        `json:"dir"`
	Display      string        `json:"display"`
	Kind         AppsKind      `json:"kind"`
	Environments []string      `json:"environments"`
	Clusters     []AppsCluster `json:"clusters"`
	Branch       string        `json:"branch"`
	Remote       string        `json:"remote"`
	URL          string        `json:"url"`
}

type AppsConnect struct {
	Cluster       string
	Environment   string
	Environments  []string
	Path          string
	Operator      []string
	FluxRecipient string
}

type AppsFile struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Action string `json:"action"`
}

const (
	FileCreate = "create"
	FileEdit   = "edit"
)

var (
	envRE        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	ignoredEmpty = regexp.MustCompile(`^(\..*|README.*|LICENSE.*)$`)
)

func InspectApps(ctx context.Context, dir string) (AppsRepo, error) {
	a := AppsRepo{Dir: dir, Display: Tilde(dir), Environments: []string{}, Clusters: []AppsCluster{}}
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return a, fmt.Errorf("%s is not a git checkout", Tilde(dir))
	}
	if top = strings.TrimSpace(top); !sameDir(top, dir) {
		return a, fmt.Errorf("%s is inside the checkout %s; choose its top folder", Tilde(dir), Tilde(top))
	}
	remote, err := git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return a, fmt.Errorf("%s has no origin remote", Tilde(dir))
	}
	a.Remote = strings.TrimSpace(remote)
	url, ok := FluxURL(a.Remote)
	if !ok {
		return a, fmt.Errorf("origin %q is not a URL Flux can clone", a.Remote)
	}
	if !gitremote.IsSSH(url) && gitremote.Private(ctx, url) {
		if u, ok := SSHForm(a.Remote); ok {
			url = u
		}
	}
	a.URL = url
	if b, err := git(ctx, dir, "symbolic-ref", "--short", "HEAD"); err == nil {
		a.Branch = strings.TrimSpace(b)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return a, err
	}
	a.Kind = AppsEmpty
	for _, e := range entries {
		if !ignoredEmpty.MatchString(e.Name()) {
			a.Kind = AppsOther
		}
	}
	if isDir(filepath.Join(dir, "apps")) && isDir(filepath.Join(dir, "clusters")) {
		a.Kind = AppsLayout
		a.Environments = subdirs(filepath.Join(dir, "apps"), "base")
		for _, c := range subdirs(filepath.Join(dir, "clusters")) {
			a.Clusters = append(a.Clusters, AppsCluster{Name: c, Environment: a.envOf(c)})
		}
	}
	return a, nil
}

func (a AppsRepo) envOf(cluster string) string {
	for _, p := range fluxPaths(filepath.Join(a.Dir, "clusters", cluster, "apps.yaml")) {
		if env, ok := strings.CutPrefix(p, "apps/"); ok {
			return env
		}
	}
	return ""
}

func (a AppsRepo) FluxPath(c AppsConnect) string {
	if a.Kind == AppsOther {
		if root := FluxRoot(c.Path); root != "" {
			return "./" + root
		}
		return "./"
	}
	return "./clusters/" + c.Cluster
}

func (a AppsRepo) check(c AppsConnect) error {
	if a.Kind == AppsOther {
		if strings.HasPrefix(c.Path, "/") || slices.Contains(strings.Split(c.Path, "/"), "..") {
			return fmt.Errorf("path %q must be relative to the repository root", c.Path)
		}
		return nil
	}
	envs := a.Environments
	if a.Kind == AppsEmpty {
		envs = c.Environments
		if len(envs) == 0 {
			return errors.New("choose at least one environment")
		}
	}
	for _, e := range append(slices.Clone(envs), c.Environment) {
		if !envRE.MatchString(e) || e == "base" {
			return fmt.Errorf("environment %q: use lowercase letters, digits and hyphens", e)
		}
	}
	if a.Kind == AppsEmpty && !slices.Contains(envs, c.Environment) {
		return fmt.Errorf("environment %q is not one of %s", c.Environment, strings.Join(envs, ", "))
	}
	for _, cl := range a.Clusters {
		if cl.Name == c.Cluster && cl.Environment != "" && cl.Environment != c.Environment {
			return fmt.Errorf("clusters/%s already serves %s", c.Cluster, cl.Environment)
		}
	}
	return nil
}

func (a AppsRepo) contents(c AppsConnect) (map[string][]byte, []string, error) {
	files := map[string][]byte{}
	envs := []string{c.Environment}
	if a.Kind == AppsEmpty {
		envs = c.Environments
	}
	base, err := kustomizationFile(nil)
	if err != nil {
		return nil, nil, err
	}
	overlay, err := kustomizationFile([]string{"../base"})
	if err != nil {
		return nil, nil, err
	}
	for _, area := range []string{"infrastructure", "apps"} {
		files[area+"/base/kustomization.yaml"] = base
		for _, env := range envs {
			files[area+"/"+env+"/kustomization.yaml"] = overlay
		}
	}
	dirC := "clusters/" + c.Cluster
	if files[dirC+"/kustomization.yaml"], err = kustomizationFile([]string{"infrastructure.yaml", "apps.yaml"}); err != nil {
		return nil, nil, err
	}
	if files[dirC+"/infrastructure.yaml"], err = fluxKustomization("infrastructure", "infrastructure/"+c.Environment, ""); err != nil {
		return nil, nil, err
	}
	if files[dirC+"/apps.yaml"], err = fluxKustomization("apps", "apps/"+c.Environment, "infrastructure"); err != nil {
		return nil, nil, err
	}
	return files, envs, nil
}

func (a AppsRepo) Files(c AppsConnect) ([]AppsFile, error) {
	if err := a.check(c); err != nil {
		return nil, err
	}
	if a.Kind == AppsOther {
		return []AppsFile{}, nil
	}
	files, _, err := a.contents(c)
	if err != nil {
		return nil, err
	}
	out := []AppsFile{}
	cfg := filepath.Join(a.Dir, sops.ConfigFile)
	switch {
	case !exists(cfg):
		out = append(out, AppsFile{Repo: "apps", Path: sops.ConfigFile, Action: FileCreate})
	case !a.envReads(c):
		out = append(out, AppsFile{Repo: "apps", Path: sops.ConfigFile, Action: FileEdit})
	}
	for _, p := range sortedKeys(files) {
		if !exists(filepath.Join(a.Dir, filepath.FromSlash(p))) {
			out = append(out, AppsFile{Repo: "apps", Path: p, Action: FileCreate})
		}
	}
	return out, nil
}

func (a AppsRepo) envReads(c AppsConnect) bool {
	for _, area := range []string{"apps", "infrastructure"} {
		r, err := sops.RuleFor(a.Dir, filepath.Join(a.Dir, area, c.Environment, "example.sops.yaml"))
		if err != nil || !slices.Contains(r.Age, c.FluxRecipient) {
			return false
		}
	}
	return true
}

func ConnectApps(a AppsRepo, c AppsConnect) error {
	if err := a.check(c); err != nil {
		return err
	}
	if a.Kind == AppsOther {
		return nil
	}
	files, envs, err := a.contents(c)
	if err != nil {
		return err
	}
	cfg := filepath.Join(a.Dir, sops.ConfigFile)
	if !exists(cfg) {
		b, err := sops.AppsConfig(c.Operator, envs, SecretsFile)
		if err != nil {
			return err
		}
		if err := create(cfg, 0o644, b); err != nil {
			return err
		}
	}
	if _, err := sops.AddEnvRule(a.Dir, c.Environment, c.Operator); err != nil {
		return err
	}
	samples := []string{"apps/" + c.Environment + "/" + sampleSecret, "infrastructure/" + c.Environment + "/" + sampleSecret}
	if _, err := sops.AddRecipient(a.Dir, c.FluxRecipient, SecretsFile, samples); err != nil {
		return err
	}
	for _, p := range sortedKeys(files) {
		full := filepath.Join(a.Dir, filepath.FromSlash(p))
		if exists(full) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := create(full, 0o644, files[p]); err != nil {
			return err
		}
	}
	return nil
}

func kustomizationFile(resources []string) ([]byte, error) {
	if resources == nil {
		resources = []string{}
	}
	return yamlx.Encode(struct {
		APIVersion string   `yaml:"apiVersion"`
		Kind       string   `yaml:"kind"`
		Resources  []string `yaml:"resources"`
	}{"kustomize.config.k8s.io/v1beta1", "Kustomization", resources})
}

type fluxRef struct {
	Name string `yaml:"name"`
}

func fluxKustomization(name, path, dependsOn string) ([]byte, error) {
	type spec struct {
		DependsOn []fluxRef `yaml:"dependsOn,omitempty"`
		Interval  string    `yaml:"interval"`
		Path      string    `yaml:"path"`
		Prune     bool      `yaml:"prune"`
		SourceRef struct {
			Kind string `yaml:"kind"`
			Name string `yaml:"name"`
		} `yaml:"sourceRef"`
		Decryption struct {
			Provider  string  `yaml:"provider"`
			SecretRef fluxRef `yaml:"secretRef"`
		} `yaml:"decryption"`
	}
	s := spec{Interval: "10m", Path: "./" + path, Prune: true}
	if dependsOn != "" {
		s.DependsOn = []fluxRef{{Name: dependsOn}}
	}
	s.SourceRef.Kind, s.SourceRef.Name = "GitRepository", tofu.FluxSource
	s.Decryption.Provider, s.Decryption.SecretRef.Name = "sops", tofu.SOPSSecret
	return yamlx.Encode(struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec spec `yaml:"spec"`
	}{APIVersion: "kustomize.toolkit.fluxcd.io/v1", Kind: "Kustomization", Metadata: struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	}{name, tofu.SOPSNamespace}, Spec: s})
}

func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}

func sameDir(a, b string) bool {
	x, err1 := filepath.EvalSymlinks(a)
	y, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && x == y
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func subdirs(dir string, skip ...string) []string {
	out := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !slices.Contains(skip, e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func sortedKeys(m map[string][]byte) []string {
	return slices.Sorted(maps.Keys(m))
}

func Connect(ctx context.Context, clusterDir, hash string, a AppsRepo, c AppsConnect) error {
	operator, _, err := Recipients()
	if err != nil {
		return err
	}
	c.Operator = operator
	if err := ConnectApps(a, c); err != nil {
		return err
	}
	if hash != "" {
		cur, _, err := LoadSpec(clusterDir)
		if err != nil {
			return err
		}
		fr := &config.FluxRepository{URL: a.URL, Branch: a.Branch, Path: a.FluxPath(c)}
		if old := cur.Spec.Platform.Flux.Repository; old != nil {
			fr.Interval = old.Interval
		}
		if err := SetAddon(clusterDir, hash, "flux", AddonEdit{Enabled: true, Repository: fr}); err != nil {
			return err
		}
	}
	if !gitremote.IsSSH(a.URL) {
		return nil
	}
	ids, err := sops.Identities()
	if err != nil {
		return err
	}
	dk, err := DeployKeyOf(clusterDir, ids)
	if err != nil {
		return err
	}
	if dk.Key != nil && gitremote.Pinned(dk.Hosts, a.URL) {
		return nil
	}
	return NewDeployKey(ctx, clusterDir, dk.Hash, dk.Key != nil, ids)
}
