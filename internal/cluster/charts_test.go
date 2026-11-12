package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mikaelhug/kubit/internal/tofu"
)

func TestPlanStepsLonghornOneMinorPerApplyAndRefusesAKubeVersionItExcludes(t *testing.T) {
	saved := tofu.Charts
	t.Cleanup(func() { tofu.Charts = saved })
	tofu.Charts = map[string]tofu.Chart{"longhorn": {StepMinors: true, Steps: []tofu.Step{
		{Version: "1.10.1", KubeVersion: ">=1.25.0-0"},
		{Version: "1.11.3", KubeVersion: ">=1.25.0-0"},
		{Version: "1.12.2", KubeVersion: ">=1.36.0-0"},
	}}}

	installed := ""
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"gitVersion": "v1.35.1", "major": "1", "minor": "35"})
		case "/api/v1/namespaces/longhorn-system/secrets":
			items := []any{}
			if installed != "" {
				rel, _ := json.Marshal(map[string]any{"name": "longhorn", "version": 1, "info": map[string]string{"status": "deployed"}, "chart": map[string]any{"metadata": map[string]string{"name": "longhorn", "version": installed}}})
				items = append(items, map[string]any{
					"metadata": map[string]any{"name": "sh.helm.release.v1.longhorn.v1", "labels": map[string]string{"owner": "helm", "name": "longhorn", "version": "1"}},
					"data":     map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(rel))},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "SecretList", "apiVersion": "v1", "items": items})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "SecretList", "apiVersion": "v1", "items": []any{}})
		}
	}))
	t.Cleanup(api.Close)

	c := parsedSpec(t, convergeSpec)
	c.Spec.Platform.Longhorn.Enabled = true
	m := NewManager(testStore(t), t.TempDir())
	plan := func(at string) chartTargets {
		t.Helper()
		installed = at
		got, err := m.chartTargets(context.Background(), c, kubeconfigFor(api.URL))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := plan("1.10.1"); got.versions["longhorn"] != "1.11.3" || got.rows["longhorn"] != "1.10.1 → 1.11.3 (1 of 2)" || len(got.problems) > 0 {
		t.Errorf("from 1.10.1: target %q, row %q, problems %v; Longhorn refuses a skipped minor", got.versions["longhorn"], got.rows["longhorn"], got.problems)
	}
	if got := plan("1.11.3"); !slices.Contains(got.problems, "longhorn 1.12.2 needs Kubernetes >=1.36.0.") {
		t.Errorf("from 1.11.3 on Kubernetes 1.35: problems %v; a kubeVersion the cluster fails must refuse the plan", got.problems)
	}
	if got := plan("1.8.4"); len(got.problems) != 1 {
		t.Errorf("from 1.8.4: problems %v; two minors behind the oldest step cannot be stepped", got.problems)
	}
}
