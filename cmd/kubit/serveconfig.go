package main

import (
	"fmt"
	"os"

	"github.com/mikael/kubit/internal/store"
	"sigs.k8s.io/yaml"
)

const envSMTPPassword = "KUBIT_SMTP_PASSWORD"

func loadServeConfig(path string) (store.Settings, error) {
	v := store.DefaultSettings()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return v, err
		}
		if err := yaml.UnmarshalStrict(b, &v); err != nil {
			return v, fmt.Errorf("%s: %w", path, err)
		}
	}
	if v.Alerts.SMTP.Password != "" {
		return v, fmt.Errorf("%s: set the SMTP password in %s, not in the file", path, envSMTPPassword)
	}
	v.Alerts.SMTP.Password = os.Getenv(envSMTPPassword)
	return v, nil
}
