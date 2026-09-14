package main

import (
	"fmt"
	"os"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "kubit",
		Short:         "Declarative Talos/Kubernetes cluster lifecycle manager",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(versionCmd(), serveCmd(), configCmd(), discoverCmd(), clusterCmd(), nodeCmd(), platformCmd(), upgradeCmd(), statusCmd(), pxeCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print kubit, Talos and default Kubernetes versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "kubit %s\ntalos %s\nkubernetes %s\n",
				version, gendata.VersionTag, constants.DefaultKubernetesVersion)
			return err
		},
	}
}
