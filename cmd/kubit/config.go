package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/factory"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Work with cluster.yaml"}
	cmd.AddCommand(configRenderCmd(), configValidateCmd())
	return cmd
}

func configValidateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Parse and validate a cluster.yaml, printing the defaulted result",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(file)
			if err != nil {
				return err
			}
			b, err := c.Marshal()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "cluster.yaml", "cluster declaration")
	return cmd
}

func configRenderCmd() *cobra.Command {
	var file, out, schematic string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Generate fresh secrets, talosconfig and per-node machine configs into a directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load(file)
			if err != nil {
				return err
			}
			f := factory.New()
			if schematic == "" {
				schematic = c.Spec.SchematicID
			}
			if schematic == "" {
				if schematic, err = f.CreateSchematic(cmd.Context(), c.Spec.Extensions); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "schematic %s\n", schematic)
			}
			g, err := config.Generate(c, nil, f.InstallerImage(schematic, c.Spec.TalosVersion))
			if err != nil {
				return err
			}
			if err := os.MkdirAll(out, 0o700); err != nil {
				return err
			}
			secretsYAML, err := yaml.Marshal(g.Secrets)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, "secrets.yaml"), secretsYAML, 0o600); err != nil {
				return err
			}
			if err := g.Talosconfig.Save(filepath.Join(out, "talosconfig")); err != nil {
				return err
			}
			for host, b := range g.Nodes {
				if err := os.WriteFile(filepath.Join(out, host+".yaml"), b, 0o600); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote secrets.yaml, talosconfig and %d machine configs to %s\n", len(g.Nodes), out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "cluster.yaml", "cluster declaration")
	cmd.Flags().StringVarP(&out, "out", "o", "out", "output directory")
	cmd.Flags().StringVar(&schematic, "schematic", "", "Image Factory schematic ID (default: create from spec.extensions)")
	return cmd
}
