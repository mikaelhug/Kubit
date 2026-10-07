package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/gitremote"
	"github.com/mikaelhug/kubit/internal/repo"
)

const AppsProblem = "Apps repository: "

func (m *Manager) fluxSourceProblems(ctx context.Context, c *config.Cluster, d *Desired) []string {
	r := c.Spec.Platform.Flux.Repository
	if !c.Spec.Platform.Flux.Enabled || r == nil {
		return nil
	}
	var err error
	if gitremote.IsSSH(r.URL) {
		err = gitremote.Probe(ctx, r.URL, r.Branch, d.GitKey, d.KnownHosts)
	} else {
		err = gitremote.ProbeHTTPS(ctx, r.URL, r.Branch)
	}
	var out []string
	var refusal *gitremote.Refusal
	if errors.As(err, &refusal) {
		out = append(out, AppsProblem+refusal.Msg)
	}
	if m.Checkout == nil {
		return out
	}
	dir := m.Checkout(r.URL)
	if dir == "" {
		return out
	}
	root := repo.FluxRoot(r.Path)
	paths := []string{root}
	for _, p := range repo.FluxRoots(dir, root) {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	if missing := repo.Unpushed(ctx, dir, r.Branch, paths); len(missing) > 0 {
		out = append(out, fmt.Sprintf("%scommit and push %s to origin/%s in %s", AppsProblem, strings.Join(missing, ", "), r.Branch, repo.Tilde(dir)))
	}
	return out
}
