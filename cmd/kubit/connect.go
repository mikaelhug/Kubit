package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/sops"
	"github.com/spf13/cobra"
)

type appsFlags struct {
	env, path string
	envs      []string
}

func (f *appsFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.env, "env", "", "environment this cluster serves")
	cmd.Flags().StringSliceVar(&f.envs, "envs", nil, "environments to create in an empty apps checkout (default: --env)")
	cmd.Flags().StringVar(&f.path, "path", "./", "Flux path, for a checkout without Kubit's layout")
}

func (f *appsFlags) connect(ctx context.Context, checkout, cluster, fluxRecipient string) (repo.AppsRepo, repo.AppsConnect, []repo.AppsFile, error) {
	dir, err := filepath.Abs(checkout)
	if err != nil {
		return repo.AppsRepo{}, repo.AppsConnect{}, nil, err
	}
	a, err := repo.InspectApps(ctx, dir)
	if err != nil {
		return a, repo.AppsConnect{}, nil, err
	}
	envs := f.envs
	if len(envs) == 0 && f.env != "" {
		envs = []string{f.env}
	}
	c := repo.AppsConnect{Cluster: cluster, Environment: f.env, Environments: envs, Path: f.path, FluxRecipient: fluxRecipient}
	files, err := a.Files(c)
	return a, c, files, err
}

func printConnected(out io.Writer, clusterDir string, a repo.AppsRepo, c repo.AppsConnect, files []repo.AppsFile) error {
	for _, f := range files {
		base := a.Dir
		if f.Repo == "cluster" {
			base = clusterDir
		}
		fmt.Fprintf(out, "%s %s\n", map[string]string{repo.FileCreate: "+", repo.FileEdit: "~"}[f.Action], repo.Tilde(filepath.Join(base, f.Path)))
	}
	fmt.Fprintf(out, "Flux syncs %s (%s) at %s\n", a.URL, a.Branch, a.FluxPath(c))
	ids, err := sops.Identities()
	if err != nil {
		return err
	}
	if dk, err := repo.DeployKeyOf(clusterDir, ids); err == nil && dk.Key != nil {
		fmt.Fprintf(out, "Deploy key (add it read-only to the repository):\n%s\n", dk.Public)
	}
	fmt.Fprintf(out, "Commit and push %s, commit %s, then run kubit apply.\n", repo.Tilde(a.Dir), repo.Tilde(clusterDir))
	return nil
}

func connectCmd() *cobra.Command {
	var f appsFlags
	cmd := &cobra.Command{
		Use:   "connect <cluster-dir> <apps-checkout>",
		Short: "Connect a cluster to an environment of a Flux apps repository and write the layout Flux needs",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			r, err := repo.Load(args[0])
			if err != nil {
				return err
			}
			a, c, files, err := f.connect(ctx, args[1], r.Cluster.Metadata.Name, r.Secrets.FluxRecipient())
			if err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(args[0], repo.ClusterFile))
			if err != nil {
				return err
			}
			if err := repo.Connect(ctx, args[0], repo.Fingerprint(b), a, c); err != nil {
				return err
			}
			return printConnected(cmd.OutOrStdout(), args[0], a, c, append(files, repo.AppsFile{Repo: "cluster", Path: repo.ClusterFile, Action: repo.FileEdit}))
		},
	}
	f.register(cmd)
	return cmd
}
