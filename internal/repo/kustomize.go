package repo

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mikael/kubit/internal/yamlx"
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
