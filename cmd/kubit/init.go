package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/repo"
	"github.com/mikaelhug/kubit/internal/sops"
	"github.com/mikaelhug/kubit/internal/talos"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	var (
		name         string
		nodes        []string
		recipients   []string
		talosVersion string
		lbRange      string
		timeout      time.Duration
		apps         string
		af           appsFlags
	)
	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "Write cluster.yaml and encrypted secrets for a new cluster into a repo directory",
		Long: `Probes the machines in Talos maintenance mode at --nodes, proposes cluster.yaml for them,
generates the Talos secrets and writes them SOPS-encrypted to secrets.sops.yaml for the
age recipients in .sops.yaml (or --age, or your own key). Never overwrites.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			if name == "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				name = config.NameFrom(filepath.Base(abs))
			}
			if err := config.CheckName(name); err != nil {
				return err
			}
			if len(nodes) == 0 {
				return errors.New("--nodes: give the addresses (or subnets) of the machines in Talos maintenance mode")
			}
			addrs, err := talos.ExpandTargets(nodes)
			if err != nil {
				return err
			}
			var machines []config.Machine
			for _, r := range talos.Scan(cmd.Context(), addrs, 64, timeout) {
				if r.Err == nil && r.State == talos.StateMaintenance && r.Inventory != nil {
					machines = append(machines, repo.Machine(r.IP, r.Inventory))
				}
			}
			if len(machines) == 0 {
				return fmt.Errorf("no machine in Talos maintenance mode at %v", nodes)
			}
			c, warnings := config.Design(name, machines, config.DesignOptions{MetalLBRange: lbRange})
			if talosVersion != "" {
				c.Spec.TalosVersion = talosVersion
			}
			if len(recipients) == 0 {
				var created string
				if recipients, created, err = repo.Recipients(); err != nil {
					return err
				}
				if created != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "created your age key %s; back it up, it opens the cluster secrets\n", created)
				}
			}
			var a repo.AppsRepo
			var conn repo.AppsConnect
			var files []repo.AppsFile
			if apps != "" {
				if a, conn, files, err = af.connect(cmd.Context(), apps, name, ""); err != nil {
					return err
				}
				c.Spec.Platform.Flux.Enabled = true
				c.Spec.Platform.Flux.Repository = &config.FluxRepository{URL: a.URL, Branch: a.Branch, Path: a.FluxPath(conn)}
			}
			r, err := repo.Init(dir, c, recipients)
			if err != nil {
				return err
			}
			if apps != "" {
				conn.FluxRecipient = r.Secrets.FluxRecipient()
				if err := repo.Connect(cmd.Context(), dir, "", a, conn); err != nil {
					return err
				}
				defer printConnected(cmd.OutOrStdout(), dir, a, conn, files)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "wrote %s with %d node(s), %s, %s\n", filepath.Join(dir, repo.ClusterFile), len(c.Spec.Nodes), repo.SecretsFile, sops.ConfigFile)
			for _, w := range warnings {
				fmt.Fprintf(out, "  %s %s: %s\n", w.Level, w.Code, w.Message)
			}
			fmt.Fprintf(out, "Flux decrypts app secrets for %s\n", r.Secrets.FluxRecipient())
			fmt.Fprintln(out, "Review cluster.yaml, commit, then run kubit apply.")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "cluster name (default: the directory name)")
	cmd.Flags().StringSliceVar(&nodes, "nodes", nil, "addresses or subnets of the machines in maintenance mode")
	cmd.Flags().StringSliceVar(&recipients, "age", nil, "age recipients for the secrets (default: .sops.yaml, else your own key)")
	cmd.Flags().StringVar(&talosVersion, "talos-version", "", "Talos version (default: Kubit's)")
	cmd.Flags().StringVar(&lbRange, "lb-range", "", "LoadBalancer address range start-end; turns on MetalLB and Traefik")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "per-host connect timeout")
	cmd.Flags().StringVar(&apps, "apps", "", "Flux apps repository checkout to connect")
	af.register(cmd)
	return cmd
}

func credentialCmd(use, short string, derive func(*repo.Repo) ([]byte, error)) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   use + " [dir]",
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			r, err := repo.Load(dir)
			if err != nil {
				return err
			}
			b, err := derive(r)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(b)
				return err
			}
			return os.WriteFile(out, b, 0o600)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default: stdout)")
	return cmd
}
