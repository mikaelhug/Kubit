package cluster

import "github.com/mikaelhug/kubit/internal/repo"

func FromRepo(r *repo.Repo) (*Desired, error) {
	tc, err := r.Talosconfig()
	if err != nil {
		return nil, err
	}
	kc, err := r.Kubeconfig()
	if err != nil {
		return nil, err
	}
	return &Desired{Dir: r.Dir, Cluster: r.Cluster, Bundle: r.Secrets.Bundle, Talosconfig: tc, Kubeconfig: kc, FluxKey: r.Secrets.FluxKey, GitKey: r.Secrets.DeployKey, KnownHosts: r.Secrets.KnownHosts, RepoState: r.StatePath(), Digest: r.Digest, Passphrase: r.Secrets.StatePassphrase, BackupKey: r.Secrets.BackupKeyID, BackupSec: r.Secrets.BackupSecret}, nil
}
