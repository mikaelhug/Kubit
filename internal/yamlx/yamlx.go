package yamlx

import (
	"bytes"

	"go.yaml.in/yaml/v4"
)

func Encode(v any) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Lookup(m *yaml.Node, key string) (*yaml.Node, int) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

func Value(m *yaml.Node, key string) *yaml.Node {
	v, _ := Lookup(m, key)
	return v
}
