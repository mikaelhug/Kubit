package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mikael/kubit/internal/repo"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/gendata"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	adoptInvokingUserHome()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	err := rootCmd().ExecuteContext(ctx)
	stop()
	var code exitCode
	if errors.As(err, &code) {
		os.Exit(int(code))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	console := serveCmd(true)
	root := &cobra.Command{
		Use:           "kubit",
		Short:         "Declarative Talos clusters from a repo; kubit [dirs...] runs the daemon and opens the console",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE:          console.RunE,
	}
	root.Flags().AddFlagSet(console.Flags())
	root.AddCommand(versionCmd(), serveCmd(false), initCmd(), planCmd(), applyCmd(), exportCmd(),
		credentialCmd("talosconfig", "Print an admin talosconfig derived from the repo's secrets", (*repo.Repo).Talosconfig),
		credentialCmd("kubeconfig", "Print an admin kubeconfig derived from the repo's secrets", (*repo.Repo).Kubeconfig), etcdCmd(), pxeCmd())
	root.CompletionOptions.DisableDefaultCmd = true
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
