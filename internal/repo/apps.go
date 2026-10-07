package repo

import (
	"context"
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

type AppsRepo struct {
	Dir      string   `json:"dir"`
	Display  string   `json:"display"`
	Kind     AppsKind `json:"kind"`
	Clusters []string `json:"clusters"`
	Branch   string   `json:"branch"`
	Remote   string   `json:"remote"`
	URL      string   `json:"url"`
}

type AppsConnect struct {
	Cluster         string
	Path            string
	ImageAutomation bool
	Operator        []string
	FluxRecipient   string
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
	a := AppsRepo{Dir: dir, Display: Tilde(dir), Clusters: []string{}}
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
		a.Clusters = subdirs(filepath.Join(dir, "clusters"))
	}
	return a, nil
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
	if !envRE.MatchString(c.Cluster) || c.Cluster == "base" {
		return fmt.Errorf("cluster name %q cannot name a folder in the apps repository", c.Cluster)
	}
	return nil
}

func (a AppsRepo) contents(c AppsConnect) (map[string][]byte, error) {
	files := map[string][]byte{}
	empty, err := kustomizationFile(nil)
	if err != nil {
		return nil, err
	}
	for _, area := range []string{"infrastructure", "apps"} {
		files[area+"/"+c.Cluster+"/kustomization.yaml"] = empty
	}
	dirC := "clusters/" + c.Cluster
	listed := []string{"infrastructure.yaml", "apps.yaml"}
	if c.ImageAutomation {
		listed = append(listed, imageAutomationFile)
		if files[dirC+"/"+imageAutomationFile], err = imageAutomation(c.Cluster, a.Branch); err != nil {
			return nil, err
		}
	}
	if files[dirC+"/kustomization.yaml"], err = kustomizationFile(listed); err != nil {
		return nil, err
	}
	if files[dirC+"/infrastructure.yaml"], err = fluxKustomization("infrastructure", "infrastructure/"+c.Cluster, ""); err != nil {
		return nil, err
	}
	if files[dirC+"/apps.yaml"], err = fluxKustomization("apps", "apps/"+c.Cluster, "infrastructure"); err != nil {
		return nil, err
	}
	return files, nil
}

func (a AppsRepo) Files(c AppsConnect) ([]AppsFile, error) {
	if err := a.check(c); err != nil {
		return nil, err
	}
	if a.Kind == AppsOther {
		return []AppsFile{}, nil
	}
	files, err := a.contents(c)
	if err != nil {
		return nil, err
	}
	out := []AppsFile{}
	cfg := filepath.Join(a.Dir, sops.ConfigFile)
	switch {
	case !exists(cfg):
		out = append(out, AppsFile{Repo: "apps", Path: sops.ConfigFile, Action: FileCreate})
	case !a.clusterReads(c):
		out = append(out, AppsFile{Repo: "apps", Path: sops.ConfigFile, Action: FileEdit})
	}
	for _, p := range sortedKeys(files) {
		if !exists(filepath.Join(a.Dir, filepath.FromSlash(p))) {
			out = append(out, AppsFile{Repo: "apps", Path: p, Action: FileCreate})
		}
	}
	return out, nil
}

func (a AppsRepo) clusterReads(c AppsConnect) bool {
	for _, area := range []string{"apps", "infrastructure"} {
		r, err := sops.RuleFor(a.Dir, filepath.Join(a.Dir, area, c.Cluster, "example.sops.yaml"))
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
	files, err := a.contents(c)
	if err != nil {
		return err
	}
	cfg := filepath.Join(a.Dir, sops.ConfigFile)
	if !exists(cfg) {
		b, err := sops.AppsConfig(c.Operator, SecretsFile)
		if err != nil {
			return err
		}
		if err := create(cfg, 0o644, b); err != nil {
			return err
		}
	}
	if _, err := sops.AddClusterRule(a.Dir, c.Cluster, c.Operator); err != nil {
		return err
	}
	samples := []string{"apps/" + c.Cluster + "/" + sampleSecret, "infrastructure/" + c.Cluster + "/" + sampleSecret}
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
	if c.ImageAutomation {
		_, err := ListResource(a.Dir, "clusters/"+c.Cluster+"/"+imageAutomationFile)
		return err
	}
	return nil
}

const imageAutomationFile = "image-automation.yaml"

func imageAutomation(cluster, branch string) ([]byte, error) {
	type ref struct {
		Branch string `yaml:"branch"`
	}
	type author struct {
		Name  string `yaml:"name"`
		Email string `yaml:"email"`
	}
	var doc struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			Interval  string `yaml:"interval"`
			SourceRef struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
			} `yaml:"sourceRef"`
			Git struct {
				Checkout struct {
					Ref ref `yaml:"ref"`
				} `yaml:"checkout"`
				Commit struct {
					Author author `yaml:"author"`
				} `yaml:"commit"`
				Push ref `yaml:"push"`
			} `yaml:"git"`
			Update struct {
				Path     string `yaml:"path"`
				Strategy string `yaml:"strategy"`
			} `yaml:"update"`
		} `yaml:"spec"`
	}
	doc.APIVersion, doc.Kind = "image.toolkit.fluxcd.io/v1", "ImageUpdateAutomation"
	doc.Metadata.Name, doc.Metadata.Namespace = "apps", tofu.SOPSNamespace
	doc.Spec.Interval = "1m"
	doc.Spec.SourceRef.Kind, doc.Spec.SourceRef.Name = "GitRepository", tofu.FluxSource
	doc.Spec.Git.Checkout.Ref.Branch, doc.Spec.Git.Push.Branch = branch, branch
	doc.Spec.Git.Commit.Author = author{Name: "flux-" + cluster, Email: "flux-" + cluster + "@users.noreply.github.com"}
	doc.Spec.Update.Path, doc.Spec.Update.Strategy = "./apps/"+cluster, "Setters"
	return yamlx.Encode(doc)
}

func SetImageAutomation(dir, cluster, branch string, on bool) error {
	rel := "clusters/" + cluster + "/" + imageAutomationFile
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if !on {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
		_, err := UnlistResource(dir, rel)
		return err
	}
	if !exists(full) {
		b, err := imageAutomation(cluster, branch)
		if err != nil {
			return err
		}
		if err := create(full, 0o644, b); err != nil {
			return err
		}
	}
	_, err := ListResource(dir, rel)
	return err
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

func ClusterDirs(dir string) []string {
	if exists(filepath.Join(dir, ClusterFile)) {
		return []string{dir}
	}
	var out []string
	for _, sub := range subdirs(dir) {
		if p := filepath.Join(dir, sub); exists(filepath.Join(p, ClusterFile)) {
			out = append(out, p)
		}
	}
	return out
}

func CheckoutDir(clusterDir, checkout string) string {
	if rest, ok := strings.CutPrefix(checkout, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	if filepath.IsAbs(checkout) {
		return filepath.Clean(checkout)
	}
	return filepath.Join(clusterDir, checkout)
}

func RelCheckout(clusterDir, appsDir string) string {
	if rel, err := filepath.Rel(clusterDir, appsDir); err == nil {
		return filepath.ToSlash(rel)
	}
	return Tilde(appsDir)
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
		fr := &config.FluxRepository{URL: a.URL, Branch: a.Branch, Path: a.FluxPath(c), Checkout: RelCheckout(clusterDir, a.Dir)}
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
