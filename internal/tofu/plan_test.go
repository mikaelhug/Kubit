package tofu_test

import (
	"os"
	"testing"

	"github.com/mikael/kubit/internal/tofu"
)

func TestParseShowPlanReadsOpenTofuPlanJSON(t *testing.T) {
	raw, err := os.ReadFile("testdata/plan-changes.json")
	if err != nil {
		t.Fatal(err)
	}
	groups, err := tofu.ParseShowPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	byAddon := map[string]string{}
	for _, g := range groups {
		byAddon[g.Addon] = g.Actions
	}
	if byAddon["cert-manager"] != "1 to create" || byAddon["metallb"] != "1 to update" || len(groups) != 2 {
		t.Errorf("groups %+v", groups)
	}
}
