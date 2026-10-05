package sops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"go.yaml.in/yaml/v4"
)

const ConfigFile = ".sops.yaml"

type creationRule struct {
	PathRegex string `yaml:"path_regex,omitempty"`
	Age       any    `yaml:"age,omitempty"`
	Rule      `yaml:",inline"`
}

type configFile struct {
	CreationRules []creationRule `yaml:"creation_rules"`
}

func RuleFor(dir, file string) (Rule, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		return Rule{}, err
	}
	var cf configFile
	if err := yaml.Unmarshal(b, &cf); err != nil {
		return Rule{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		rel = file
	}
	rel = filepath.ToSlash(rel)
	for _, cr := range cf.CreationRules {
		if cr.PathRegex != "" {
			re, err := regexp.Compile(cr.PathRegex)
			if err != nil {
				return Rule{}, fmt.Errorf("%s: path_regex: %w", ConfigFile, err)
			}
			if !re.MatchString(rel) {
				continue
			}
		}
		r := cr.Rule
		switch a := cr.Age.(type) {
		case string:
			r.Age = ParseRecipients(a)
		case []any:
			for _, x := range a {
				if s, ok := x.(string); ok {
					r.Age = append(r.Age, ParseRecipients(s)...)
				}
			}
		}
		if len(r.Age) == 0 {
			return Rule{}, fmt.Errorf("%s: the rule for %s has no age recipients", ConfigFile, rel)
		}
		return r, nil
	}
	return Rule{}, errors.New(ConfigFile + ": no creation rule matches " + rel)
}

func WriteConfig(dir string, recipients []string) error {
	cf := configFile{CreationRules: []creationRule{{PathRegex: `\.sops\.yaml$`, Age: joinRecipients(recipients)}}}
	b, err := yaml.Marshal(cf)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ConfigFile), b, 0o644)
}

func joinRecipients(r []string) string {
	out := ""
	for i, s := range r {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
