package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mikaelhug/kubit/internal/cluster"
	"github.com/spf13/cobra"
)

func destroyCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "destroy [dir]",
		Short: "Reset every node of the cluster declared in dir to Talos maintenance mode; the repo is kept",
		Args:  cobra.MaximumNArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			d, err := desired(dirArg(args))
			if err != nil {
				return err
			}
			name := d.Cluster.Metadata.Name
			out := cmd.OutOrStdout()
			for _, n := range d.Cluster.Spec.Nodes {
				fmt.Fprintf(out, "- reset %s (%s at %s): wipes STATE and EPHEMERAL\n", n.Hostname, n.Role, n.IP)
			}
			confirm := name
			if !yes {
				if confirm, err = typed(cmd.InOrStdin(), out, fmt.Sprintf("Type %s to destroy it: ", name)); err != nil {
					return err
				}
			}
			if err := cluster.CheckDestroy(d.Cluster, confirm); err != nil {
				return errors.New("not destroyed")
			}
			defer cluster.KeepAwake()()
			return m.Destroy(cmd.Context(), d, confirm, lockHolder(), printEvents(cmd))
		}),
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "destroy without asking")
	return cmd
}

func typed(in io.Reader, out io.Writer, prompt string) (string, error) {
	if err := requireTerminal(in); err != nil {
		return "", err
	}
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
