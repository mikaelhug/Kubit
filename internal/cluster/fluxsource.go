package cluster

import (
	"context"
	"errors"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/gitremote"
)

func fluxSourceProblem(ctx context.Context, c *config.Cluster, d *Desired) string {
	r := c.Spec.Platform.Flux.Repository
	if !c.Spec.Platform.Flux.Enabled || r == nil {
		return ""
	}
	var err error
	if gitremote.IsSSH(r.URL) {
		err = gitremote.Probe(ctx, r.URL, r.Branch, d.GitKey, d.KnownHosts)
	} else {
		err = gitremote.ProbeHTTPS(ctx, r.URL, r.Branch)
	}
	var refusal *gitremote.Refusal
	if errors.As(err, &refusal) {
		return refusal.Msg
	}
	return ""
}
