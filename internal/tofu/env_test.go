package tofu

import (
	"strings"
	"testing"
)

func TestChildEnvDropsKubitSecrets(t *testing.T) {
	t.Setenv("SOPS_AGE_KEY", "AGE-SECRET-KEY-1")
	t.Setenv("KUBIT_TOKEN", "secret-token")
	env := (&Runner{}).env()
	for _, kv := range env {
		if strings.HasPrefix(kv, "SOPS_AGE_KEY=") || strings.HasPrefix(kv, "KUBIT_TOKEN=") {
			t.Errorf("secret passed to tofu: %s", kv)
		}
	}
}
