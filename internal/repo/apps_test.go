package repo

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/mikaelhug/kubit/internal/sops"
)

func appsCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "git@github.com:you/apps.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func connect(t *testing.T, dir string, c AppsConnect) {
	t.Helper()
	a, err := InspectApps(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ConnectApps(a, c); err != nil {
		t.Fatal(err)
	}
}

func recipient(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String()
}

func TestConnectGivesAClusterKeyOnlyToItsOwnFolders(t *testing.T) {
	dir := appsCheckout(t)
	me, prod, stage := recipient(t), recipient(t), recipient(t)
	connect(t, dir, AppsConnect{Cluster: "production", Operator: []string{me}, FluxRecipient: prod})
	connect(t, dir, AppsConnect{Cluster: "staging", Operator: []string{me}, FluxRecipient: stage})
	for file, want := range map[string]string{
		"apps/production/db.sops.yaml":          prod,
		"infrastructure/production/s.sops.yaml": prod,
		"apps/staging/db.sops.yaml":             stage,
		"infrastructure/staging/s.sops.yaml":    stage,
		"apps/base/db.sops.yaml":                "",
		"clusters/production/x.sops.yaml":       "",
	} {
		r, err := sops.RuleFor(dir, filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !slices.Contains(r.Age, me) {
			t.Errorf("%s: you must always decrypt", file)
		}
		for _, k := range []string{prod, stage} {
			if slices.Contains(r.Age, k) != (k == want) {
				t.Errorf("%s: recipients %v, want only the %q cluster key", file, r.Age, want)
			}
		}
	}
}

func TestAppsLayoutBuildsWithKustomize(t *testing.T) {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not installed")
	}
	dir := appsCheckout(t)
	connect(t, dir, AppsConnect{Cluster: "production", Operator: []string{recipient(t)}, FluxRecipient: recipient(t)})
	for _, p := range []string{"clusters/production", "apps/production", "infrastructure/production"} {
		out, err := exec.Command(kubectl, "kustomize", filepath.Join(dir, p)).CombinedOutput()
		if err != nil {
			t.Fatalf("kustomize %s: %v\n%s", p, err, out)
		}
		if p == "clusters/production" && strings.Count(string(out), "kind: Kustomization") != 2 {
			t.Errorf("clusters/production must hold the infrastructure and apps Kustomizations:\n%s", out)
		}
	}
	if got := FluxRoots(dir, "clusters/production"); !slices.Equal(got, []string{"infrastructure/production", "apps/production"}) {
		t.Errorf("Flux roots %v", got)
	}
}
