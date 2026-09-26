package tofu

import (
	"slices"
	"strings"
	"testing"
)

func TestChildEnvDropsKubitSecrets(t *testing.T) {
	t.Setenv("KUBIT_MASTER_KEY", "secret-key")
	t.Setenv("KUBIT_TOKEN", "secret-token")
	t.Setenv("KUBIT_KEEP_ME", "yes")
	env := (&Runner{}).env()
	for _, kv := range env {
		if strings.HasPrefix(kv, "KUBIT_MASTER_KEY=") || strings.HasPrefix(kv, "KUBIT_TOKEN=") {
			t.Errorf("secret passed to tofu: %s", kv)
		}
	}
	if !slices.Contains(env, "KUBIT_KEEP_ME=yes") || !slices.Contains(env, "TF_IN_AUTOMATION=1") {
		t.Errorf("env lost unrelated variables: %v", env)
	}
	if slices.ContainsFunc(childEnv(), func(kv string) bool { return strings.HasPrefix(kv, "KUBIT_TOKEN=") }) {
		t.Error("childEnv must drop the token")
	}
}
