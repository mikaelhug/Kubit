package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
	"github.com/spf13/cobra"
)

func isRepo(arg string) bool {
	_, err := os.Stat(filepath.Join(arg, repo.ClusterFile))
	return err == nil
}

func nameOf(arg string) (string, error) {
	if !isRepo(arg) {
		if arg == "." {
			return "", errors.New("no cluster.yaml here; give a repo dir or a cluster name")
		}
		return arg, nil
	}
	c, _, err := repo.LoadSpec(arg)
	if err != nil {
		return "", err
	}
	return c.Metadata.Name, nil
}

func useCluster(cmd *cobra.Command, m *cluster.Manager, arg string) (string, error) {
	if !isRepo(arg) {
		return nameOf(arg)
	}
	r, err := repo.Load(arg)
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
