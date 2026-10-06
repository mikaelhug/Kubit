package main

import (
	"fmt"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
	"github.com/spf13/cobra"
)

func useCluster(cmd *cobra.Command, m *cluster.Manager, dir string) (string, error) {
	r, err := repo.Load(dir)
	if err != nil {
		return "", err
	}
	d, err := cluster.FromRepo(r)
	if err != nil {
		return "", err
	}
	tracked, err := m.Track(cmd.Context(), d)
	if err != nil {
		return "", err
	}
	if !tracked {
		return "", fmt.Errorf("%s is not created yet; run kubit apply", r.Cluster.Metadata.Name)
	}
	return r.Cluster.Metadata.Name, nil
}
