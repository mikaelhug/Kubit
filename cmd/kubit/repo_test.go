package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRepoHoldsNoCredentials(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git: %v", err)
	}
	named := regexp.MustCompile(`(^|/)(kubeconfig|talosconfig)(\.[^/]*)?$|(^|/)secrets[^/]*\.ya?ml$|(^|/)keys\.txt$|(^|/)master\.key$|\.(key|pem|age|kubitbak|tfstate)$`)
	secret := regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----|client-key` + `-data:|LS0tLS1CRUdJTiBFRDI1NTE5IFBSSVZBVEUg` + `S0VZ|AGE-SECRET-KEY-1[0-9A-Z]{50,}`)
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if named.MatchString(f) {
			t.Errorf("%s: credentials belong in the cluster repo, not in Kubit's", f)
			continue
		}
		if b, err := os.ReadFile(filepath.Join("../..", f)); err == nil && secret.Match(b) {
			t.Errorf("%s holds a private key or client credentials", f)
		}
	}
}
