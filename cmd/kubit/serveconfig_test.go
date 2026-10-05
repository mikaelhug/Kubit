package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeConfig(t *testing.T) {
	t.Setenv(envSMTPPassword, "hunter2")
	v, err := loadServeConfig("")
	if err != nil || v.FactoryURL != "https://factory.talos.dev" || v.Alerts.SMTP.Password != "hunter2" {
		t.Fatalf("defaults: %+v %v", v, err)
	}
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "kubit.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	v, err = loadServeConfig(write("pxeEnrollment: closed\nalerts:\n  webhookUrl: https://hooks.example/x\n  smtp: {host: mail, to: [ops@example.com]}\n"))
	if err != nil || v.PXEEnrollment != "closed" || v.Alerts.WebhookURL != "https://hooks.example/x" || v.Alerts.SMTP.Port != 587 || v.Alerts.MinSeverity != "warn" {
		t.Fatalf("file over defaults: %+v %v", v, err)
	}
	if _, err := loadServeConfig(write("alerts: {webhok: x}\n")); err == nil {
		t.Error("an unknown key must fail")
	}
	if _, err := loadServeConfig(write("alerts: {smtp: {password: x}}\n")); err == nil || !strings.Contains(err.Error(), envSMTPPassword) {
		t.Errorf("a password in the file must fail: %v", err)
	}
}
