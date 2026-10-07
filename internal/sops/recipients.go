package sops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/fsx"
	"github.com/mikaelhug/kubit/internal/yamlx"
	"go.yaml.in/yaml/v4"
)

func Rekey(data []byte, ids []age.Identity, recipients []string) ([]byte, error) {
	rule, err := RuleOf(data)
	if err != nil {
		return nil, err
	}
	plain, err := Decrypt(data, ids)
	if err != nil {
		return nil, err
	}
	rule.Age = recipients
	return Encrypt(plain, rule)
}

func SameRecipients(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

func WriteConfig(dir string, operator, apps []string, protected string) error {
	all := append(slices.Clone(operator), apps...)
	cf := configFile{CreationRules: []creationRule{
		{PathRegex: protectedRegex(protected), Age: strings.Join(operator, ",")},
		{PathRegex: catchAll, Age: strings.Join(slices.Compact(all), ",")},
	}}
	b, err := yamlx.Encode(cf)
	if err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(dir, ConfigFile), b, 0o644)
}

func AddRecipient(dir, recipient, protected string, samples []string) (bool, error) {
	return editRules(dir, func(rules *yaml.Node) (bool, error) {
		guard := slices.IndexFunc(rules.Content, func(r *yaml.Node) bool { return ruleMatches(r, protected) })
		if guard < 0 {
			return false, fmt.Errorf("%s has no rule for %s", ConfigFile, protected)
		}
		changed := false
		if slices.ContainsFunc(samples, func(s string) bool { return ruleMatches(rules.Content[guard], s) }) {
			ageNode, _ := yamlx.Lookup(rules.Content[guard], "age")
			if ageNode == nil {
				return false, fmt.Errorf("%s: the rule for %s has no age recipients", ConfigFile, protected)
			}
			own := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				scalarNode("path_regex"), scalarNode(protectedRegex(protected)),
				scalarNode("age"), copyNode(ageNode),
			}}
			rules.Content = slices.Insert(rules.Content, guard, own)
			changed = true
		}
		for _, sample := range samples {
			i := slices.IndexFunc(rules.Content, func(r *yaml.Node) bool { return ruleMatches(r, sample) })
			if i < 0 || i == guard {
				continue
			}
			if ageNode, _ := yamlx.Lookup(rules.Content[i], "age"); ageNode != nil && addTo(ageNode, recipient) {
				changed = true
			}
		}
		return changed, nil
	})
}

func ClusterRegex(cluster string) string {
	return `^(apps|infrastructure)/` + regexp.QuoteMeta(cluster) + `/.*\.sops\.ya?ml$`
}

const catchAll = `\.sops\.ya?ml$`

func AppsConfig(operator []string, protected string) ([]byte, error) {
	age := strings.Join(operator, ",")
	return yamlx.Encode(configFile{CreationRules: []creationRule{
		{PathRegex: protectedRegex(protected), Age: age},
		{PathRegex: catchAll, Age: age},
	}})
}

func AddClusterRule(dir, cluster string, operator []string) (bool, error) {
	re := ClusterRegex(cluster)
	return editRules(dir, func(rules *yaml.Node) (bool, error) {
		at := len(rules.Content)
		for i, r := range rules.Content {
			n, _ := yamlx.Lookup(r, "path_regex")
			if n != nil && n.Value == re {
				return false, nil
			}
			if (n == nil || n.Value == catchAll) && i < at {
				at = i
			}
		}
		rule := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			scalarNode("path_regex"), scalarNode(re),
			scalarNode("age"), scalarNode(strings.Join(operator, ",")),
		}}
		rules.Content = slices.Insert(rules.Content, at, rule)
		return true, nil
	})
}

func ruleMatches(rule *yaml.Node, rel string) bool {
	re, _ := yamlx.Lookup(rule, "path_regex")
	if re == nil {
		return true
	}
	ok, err := regexp.MatchString(re.Value, rel)
	return err == nil && ok
}

func editRules(dir string, edit func(rules *yaml.Node) (bool, error)) (bool, error) {
	path := filepath.Join(dir, ConfigFile)
	unlock := fsx.Lock(path)
	defer unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return false, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, errors.New(ConfigFile + " is not a mapping")
	}
	rules, _ := yamlx.Lookup(doc.Content[0], "creation_rules")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return false, errors.New(ConfigFile + " has no creation_rules")
	}
	changed, err := edit(rules)
	if err != nil || !changed {
		return false, err
	}
	out, err := yamlx.Encode(&doc)
	if err != nil {
		return false, err
	}
	return true, fsx.WriteFile(path, out, 0o644)
}

func protectedRegex(name string) string {
	return "(^|/)" + regexp.QuoteMeta(name) + "$"
}

func addTo(n *yaml.Node, recipient string) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		if slices.Contains(ParseRecipients(n.Value), recipient) {
			return false
		}
		n.Value = strings.Join(append(ParseRecipients(n.Value), recipient), ",")
		n.Style = 0
		return true
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if slices.Contains(ParseRecipients(c.Value), recipient) {
				return false
			}
		}
		n.Content = append(n.Content, scalarNode(recipient))
		return true
	}
	return false
}

func copyNode(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = copyNode(ch)
	}
	return &c
}
