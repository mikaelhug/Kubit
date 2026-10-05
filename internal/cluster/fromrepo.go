package cluster

import "github.com/mikael/kubit/internal/repo"

func FromRepo(r *repo.Repo) (*Desired, error) {
	tc, err := r.Talosconfig()
	if err != nil {
		return nil, err
	}
	kc, err := r.Kubeconfig()
	if err != nil {
		return nil, err
	}
	return &Desired{Cluster: r.Cluster, Bundle: r.Secrets.Bundle, BundleYAML: r.Secrets.BundleYAML, Talosconfig: tc, Kubeconfig: kc, FluxKey: r.Secrets.FluxKey, StatePath: r.StatePath(), Passphrase: r.Secrets.StatePassphrase}, nil
}
