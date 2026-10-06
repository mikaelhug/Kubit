package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/sops"
	"github.com/mikael/kubit/internal/yamlx"
	"go.yaml.in/yaml/v4"
)

func Recipients() (recipients []string, createdKey string, err error) {
	ids, err := sops.Identities()
	if err != nil {
		return nil, "", err
	}
	for _, id := range ids {
		if r := sops.Recipient(id); r != "" {
			recipients = append(recipients, r)
		}
	}
	if len(recipients) > 0 {
		return recipients, "", nil
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, "", err
	}
	path := sops.DefaultKeyFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, sops.KeyFileFor(id, Stamp()), 0o600); err != nil {
		return nil, "", err
	}
	return []string{id.Recipient().String()}, path, nil
}

func AddNodes(dir, hash string, add ...config.Node) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editNodes(dir, hash, func(nodes *yaml.Node) error {
		for _, n := range add {
			var entry yaml.Node
			if err := entry.Encode(n); err != nil {
				return err
			}
			nodes.Content = append(nodes.Content, &entry)
		}
		return nil
	})
}

func RemoveNode(dir, hash, hostname string) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editNodes(dir, hash, func(nodes *yaml.Node) error {
		for i, item := range nodes.Content {
			if h := yamlx.Value(item, "hostname"); h != nil && h.Value == hostname {
				nodes.Content = append(nodes.Content[:i], nodes.Content[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("%s is not in %s", hostname, ClusterFile)
	})
}

func SetNodeIP(dir, hostname, ip string) error {
	return editNode(dir, "", hostname, func(item *yaml.Node) error {
		setKey(item, "ip", &yaml.Node{Kind: yaml.ScalarNode, Value: ip})
		return nil
	})
}

func SetNodeNetwork(dir, hash, hostname string, nn *config.NodeNetwork, endpoint string) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		if err := withNode(spec, hostname, func(item *yaml.Node) error {
			if nn == nil {
				deleteKey(item, "network")
				return nil
			}
			var v yaml.Node
			if err := v.Encode(nn); err != nil {
				return err
			}
			setKey(item, "network", &v)
			return nil
		}); err != nil {
			return err
		}
		if endpoint != "" {
			cp := yamlx.Value(spec, "controlPlane")
			if cp == nil {
				cp = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				setKey(spec, "controlPlane", cp)
			}
			setKey(cp, "endpoint", &yaml.Node{Kind: yaml.ScalarNode, Value: endpoint})
		}
		return nil
	})
}

func editNode(dir, hash, hostname string, edit func(item *yaml.Node) error) error {
	return editSpec(dir, hash, func(spec *yaml.Node) error { return withNode(spec, hostname, edit) })
}

func withNode(spec *yaml.Node, hostname string, edit func(item *yaml.Node) error) error {
	nodes := yamlx.Value(spec, "nodes")
	if nodes == nil || nodes.Kind != yaml.SequenceNode {
		return errors.New("spec.nodes is not a list")
	}
	for _, item := range nodes.Content {
		if h := yamlx.Value(item, "hostname"); h != nil && h.Value == hostname {
			return edit(item)
		}
	}
	return fmt.Errorf("%s is not in %s", hostname, ClusterFile)
}

func editNodes(dir, hash string, edit func(nodes *yaml.Node) error) error {
	return editSpec(dir, hash, func(spec *yaml.Node) error {
		nodes := yamlx.Value(spec, "nodes")
		if nodes == nil {
			nodes = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			spec.Content = append(spec.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "nodes"}, nodes)
		}
		if nodes.Kind != yaml.SequenceNode {
			return errors.New("spec.nodes is not a list")
		}
		return edit(nodes)
	})
}

func editSpec(dir, hash string, edit func(spec *yaml.Node) error) error {
	path := filepath.Join(dir, ClusterFile)
	return replace(path, hash, 0o644, func(b []byte) ([]byte, error) {
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			return nil, fmt.Errorf("%s is empty", path)
		}
		spec := yamlx.Value(doc.Content[0], "spec")
		if spec == nil {
			return nil, fmt.Errorf("%s has no spec", path)
		}
		if err := edit(spec); err != nil {
			return nil, err
		}
		out, err := yamlx.Encode(&doc)
		if err != nil {
			return nil, err
		}
		if _, err := config.Parse(out); err != nil {
			return nil, err
		}
		return out, nil
	})
}

func WriteSpec(dir, hash string, data []byte, check func(*config.Cluster) error) error {
	if err := requireHash(hash); err != nil {
		return err
	}
	return replace(filepath.Join(dir, ClusterFile), hash, 0o644, func([]byte) ([]byte, error) {
		c, err := config.Parse(data)
		if err != nil {
			return nil, err
		}
		if err := check(c); err != nil {
			return nil, err
		}
		return data, nil
	})
}

func setKey(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			old := m.Content[i+1]
			v.HeadComment, v.LineComment, v.FootComment = old.HeadComment, old.LineComment, old.FootComment
			if v.Kind == yaml.ScalarNode && old.Kind == yaml.ScalarNode {
				v.Style = old.Style
			}
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
