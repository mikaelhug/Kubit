package sops

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"filippo.io/age"
	"go.yaml.in/yaml/v4"
)

type Key struct {
	Path      []string `json:"path"`
	Encrypted bool     `json:"encrypted"`
	List      bool     `json:"list,omitempty"`
}

func RuleOf(data []byte) (Rule, error) {
	_, md, err := split(data)
	if err != nil {
		return Rule{}, err
	}
	r := md.Rule
	for _, k := range md.Age {
		r.Age = append(r.Age, k.Recipient)
	}
	return r, nil
}

func Keys(data []byte) ([]Key, error) {
	root, md, err := split(data)
	if err != nil {
		return nil, err
	}
	var out []Key
	var walkKeys func(n *yaml.Node, path []string) error
	walkKeys = func(n *yaml.Node, path []string) error {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if err := walkKeys(n.Content[i+1], append(slices.Clip(path), n.Content[i].Value)); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			out = append(out, Key{Path: path, List: true})
		case yaml.ScalarNode:
			enc, err := md.encrypts(path)
			if err != nil {
				return err
			}
			out = append(out, Key{Path: path, Encrypted: enc && encPattern.MatchString(n.Value)})
		}
		return nil
	}
	return out, walkKeys(root, nil)
}

func Get(data []byte, ids []age.Identity, path []string) (string, error) {
	plain, err := Decrypt(data, ids)
	if err != nil {
		return "", err
	}
	root, err := parseMapping(plain)
	if err != nil {
		return "", err
	}
	n := lookup(root, path)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("no value at %s", strings.Join(path, "."))
	}
	return n.Value, nil
}

func Edit(data []byte, ids []age.Identity, edit func(root *yaml.Node) error) ([]byte, error) {
	rule, err := RuleOf(data)
	if err != nil {
		return nil, err
	}
	plain, err := Decrypt(data, ids)
	if err != nil {
		return nil, err
	}
	root, err := parseMapping(plain)
	if err != nil {
		return nil, err
	}
	if err := edit(root); err != nil {
		return nil, err
	}
	out, err := render(root)
	if err != nil {
		return nil, err
	}
	return Encrypt(out, rule)
}

func Set(root *yaml.Node, path []string, value string) error {
	if len(path) == 0 {
		return errors.New("empty key")
	}
	n := root
	for i, k := range path {
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("%s is not a mapping", strings.Join(path[:i], "."))
		}
		child, idx := find(n, k)
		last := i == len(path)-1
		switch {
		case child == nil && last:
			n.Content = append(n.Content, scalarNode(k), scalarNode(value))
			return nil
		case child == nil:
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content, scalarNode(k), child)
		case last:
			if child.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s holds more than one value", strings.Join(path, "."))
			}
			n.Content[idx+1] = scalarNode(value)
			return nil
		}
		n = child
	}
	return nil
}

func Delete(root *yaml.Node, path []string) error {
	if len(path) == 0 {
		return errors.New("empty key")
	}
	parent := lookup(root, path[:len(path)-1])
	if parent == nil || parent.Kind != yaml.MappingNode {
		return fmt.Errorf("no value at %s", strings.Join(path, "."))
	}
	_, i := find(parent, path[len(path)-1])
	if i < 0 {
		return fmt.Errorf("no value at %s", strings.Join(path, "."))
	}
	parent.Content = slices.Delete(parent.Content, i, i+2)
	return nil
}

func lookup(n *yaml.Node, path []string) *yaml.Node {
	for _, k := range path {
		if n.Kind != yaml.MappingNode {
			return nil
		}
		if n, _ = find(n, k); n == nil {
			return nil
		}
	}
	return n
}

func scalarNode(v string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	if strings.Contains(v, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}
