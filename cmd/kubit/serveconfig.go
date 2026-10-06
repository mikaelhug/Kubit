package main

import (
	"fmt"
	"os"

	"github.com/mikael/kubit/internal/api"
	"sigs.k8s.io/yaml"
)

func loadServeConfig(path string) (api.Settings, error) {
	v := api.DefaultSettings()
	if path == "" {
		return v, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if err := yaml.UnmarshalStrict(b, &v); err != nil {
		return v, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
