package repo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/yamlx"
	"go.yaml.in/yaml/v4"
)

var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

func IsKustomization(rel string) bool {
	return slices.Contains(kustomizationNames, path.Base(filepath.ToSlash(rel)))
}

func kustomizationIn(dir string) string {
	for _, n := range kustomizationNames {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			return filepath.Join(dir, n)
		}
	}
	return ""
}

func FluxRoot(p string) string {
	c := path.Clean(strings.TrimPrefix(filepath.ToSlash(p), "/"))
	if c == "." {
		return ""
	}
	return c
}

func FluxRoots(dir, root string) []string {
	root = FluxRoot(root)
	base := filepath.Join(dir, filepath.FromSlash(root))
	var files []string
	if k := kustomizationIn(base); k != "" {
		res, err := resources(k)
		if err != nil {
			return []string{root}
		}
		for _, r := range res {
			files = append(files, filepath.Join(base, filepath.FromSlash(r)))
		}
	} else if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			files = append(files, filepath.Join(base, e.Name()))
		}
	}
	var roots []string
	for _, f := range files {
		if ext := filepath.Ext(f); ext != ".yaml" && ext != ".yml" {
			continue
		}
		for _, p := range fluxPaths(f) {
			if !slices.Contains(roots, p) {
				roots = append(roots, p)
			}
		}
	}
	if len(roots) == 0 {
		return []string{root}
	}
	return roots
}

func fluxPaths(file string) []string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var out []string
	dec := yaml.NewDecoder(bytes.NewReader(b))
	for {
		var d struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Spec       struct {
				Path string `yaml:"path"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&d); err != nil {
			return out
		}
		if d.Kind == "Kustomization" && strings.HasPrefix(d.APIVersion, "kustomize.toolkit.fluxcd.io/") {
			out = append(out, FluxRoot(d.Spec.Path))
		}
	}
}

func RootOf(roots []string, rel string) (string, bool) {
	best, found := "", false
	for _, r := range roots {
		if (r == "" || strings.HasPrefix(rel, r+"/")) && (!found || len(r) > len(best)) {
			best, found = r, true
		}
	}
	return best, found
}

func Applied(dir, root, rel string) string {
	root = FluxRoot(root)
	rel = path.Clean(filepath.ToSlash(rel))
	inside := rel
	if root != "" {
		var ok bool
		if inside, ok = strings.CutPrefix(rel, root+"/"); !ok {
			return "outside " + root
		}
	}
	cur, explicit := root, false
	for _, next := range strings.Split(inside, "/") {
		k := kustomizationIn(filepath.Join(dir, filepath.FromSlash(cur)))
		switch {
		case k == "" && explicit:
			return cur + " has no kustomization.yaml"
		case k != "":
			explicit = true
			res, err := resources(k)
			if err != nil {
				return err.Error()
			}
			if !slices.Contains(res, next) {
				return "not in " + path.Join(cur, filepath.Base(k))
			}
		}
		cur = path.Join(cur, next)
	}
	return ""
}

func resources(file string) ([]string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var k struct {
		Resources []string `yaml:"resources"`
	}
	if err := yaml.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	out := make([]string, 0, len(k.Resources))
	for _, r := range k.Resources {
		out = append(out, path.Clean(strings.TrimPrefix(r, "./")))
	}
	return out, nil
}

func ListResource(dir, rel string) (string, error) {
	return editResources(dir, rel, func(seq *yaml.Node, base string) bool {
		for _, n := range seq.Content {
			if path.Clean(strings.TrimPrefix(n.Value, "./")) == base {
				return false
			}
		}
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: base})
		return true
	})
}

func ListPath(dir, rel string) ([]string, error) {
	rel = path.Clean(filepath.ToSlash(rel))
	var missing []string
	cur := path.Dir(rel)
	for kustomizationIn(filepath.Join(dir, filepath.FromSlash(cur))) == "" {
		if cur == "." {
			return nil, nil
		}
		missing = append(missing, cur)
		cur = path.Dir(cur)
	}
	var touched []string
	child := rel
	for _, m := range missing {
		b, err := kustomizationFile([]string{path.Base(child)})
		if err != nil {
			return touched, err
		}
		k := path.Join(m, "kustomization.yaml")
		if err := create(filepath.Join(dir, filepath.FromSlash(k)), 0o644, b); err != nil {
			return touched, err
		}
		touched = append(touched, k)
		child = m
	}
	k, err := ListResource(dir, child)
	if k != "" {
		touched = append(touched, k)
	}
	return touched, err
}

func UnlistPath(dir, rel string) ([]string, error) {
	var touched []string
	child := path.Clean(filepath.ToSlash(rel))
	for {
		k, err := UnlistResource(dir, child)
		if err != nil || k == "" {
			return touched, err
		}
		touched = append(touched, k)
		folder := path.Dir(child)
		if folder == "." || !emptyKustomization(filepath.Join(dir, filepath.FromSlash(folder))) {
			return touched, nil
		}
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(k))); err != nil {
			return touched, err
		}
		_ = os.Remove(filepath.Join(dir, filepath.FromSlash(folder)))
		child = folder
	}
}

func emptyKustomization(folder string) bool {
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 || !IsKustomization(entries[0].Name()) {
		return false
	}
	b, err := os.ReadFile(filepath.Join(folder, entries[0].Name()))
	if err != nil {
		return false
	}
	var k map[string]any
	if err := yaml.Unmarshal(b, &k); err != nil {
		return false
	}
	for key, v := range k {
		switch key {
		case "apiVersion", "kind":
		case "resources":
			if l, ok := v.([]any); !ok || len(l) > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func UnlistResource(dir, rel string) (string, error) {
	return editResources(dir, rel, func(seq *yaml.Node, base string) bool {
		before := len(seq.Content)
		seq.Content = slices.DeleteFunc(seq.Content, func(n *yaml.Node) bool { return path.Clean(strings.TrimPrefix(n.Value, "./")) == base })
		return len(seq.Content) != before
	})
}

func editResources(dir, rel string, edit func(seq *yaml.Node, base string) bool) (string, error) {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	k := kustomizationIn(filepath.Dir(full))
	if k == "" {
		return "", nil
	}
	changed := false
	err := replace(k, "", 0o644, func(b []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(k), err)
		}
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			return nil, errors.New(filepath.Base(k) + " is not a mapping")
		}
		seq := yamlx.Value(doc.Content[0], "resources")
		if seq == nil {
			seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			setKey(doc.Content[0], "resources", seq)
		}
		if seq.Kind != yaml.SequenceNode {
			return nil, errors.New(filepath.Base(k) + ": resources is not a list")
		}
		if changed = edit(seq, path.Base(filepath.ToSlash(full))); !changed {
			return nil, nil
		}
		return yamlx.Encode(&doc)
	})
	if err != nil || !changed {
		return "", err
	}
	r, _ := filepath.Rel(dir, k)
	return filepath.ToSlash(r), nil
}
