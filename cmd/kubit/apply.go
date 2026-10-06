package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
	"github.com/spf13/cobra"
)

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func desired(dir string) (*cluster.Desired, error) {
	r, err := repo.Load(dir)
	if err != nil {
		return nil, err
	}
	return cluster.FromRepo(r)
}

func dirArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "."
}

func planCmd() *cobra.Command {
	var detailed, allowRemoval, asJSON bool
	cmd := &cobra.Command{
		Use:   "plan [dir]",
		Short: "Show what kubit apply would change in the cluster declared in dir",
		Args:  cobra.MaximumNArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			d, err := desired(dirArg(args))
			if err != nil {
				return err
			}
			p, err := m.Plan(cmd.Context(), d, cluster.ConvergeOptions{AllowRemoval: allowRemoval, ReadOnly: true})
			if err != nil {
				return err
			}
			if asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(p); err != nil {
					return err
				}
			} else {
				printPlan(cmd.OutOrStdout(), p)
			}
			switch {
			case len(p.Problems) > 0:
				return exitCode(1)
			case detailed && !p.Empty():
				return exitCode(2)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "exit 2 when there are changes")
	cmd.Flags().BoolVar(&allowRemoval, "allow-removal", false, "plan node removals as allowed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan as JSON")
	return cmd
}

func applyCmd() *cobra.Command {
	var allowRemoval, yes bool
	var reviewed string
	cmd := &cobra.Command{
		Use:   "apply [dir]",
		Short: "Converge the cluster to the declaration in dir: create, join, configure, upgrade, remove, add-ons",
		Args:  cobra.MaximumNArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			ctx := cmd.Context()
			d, err := desired(dirArg(args))
			if err != nil {
				return err
			}
			opts := cluster.ConvergeOptions{AllowRemoval: allowRemoval}
			unlock, err := m.LockApply(ctx, d, lockHolder())
			if err != nil {
				return err
			}
			defer unlock()
			p, err := m.Plan(ctx, d, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			printPlan(out, p)
			if len(p.Problems) > 0 {
				return exitCode(1)
			}
			if reviewed != "" && reviewed != p.Hash {
				return fmt.Errorf("the plan is now %s, not the reviewed %s; review it again", p.Hash, reviewed)
			}
			if p.Empty() {
				return nil
			}
			if !yes {
				ok, err := confirm(cmd.InOrStdin(), out)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("not applied")
				}
			}
			defer cluster.KeepAwake()()
			if err := m.Converge(ctx, d, p, opts, printEvents(cmd)); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s converged.\n", d.Cluster.Metadata.Name)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&allowRemoval, "allow-removal", false, "remove nodes that are no longer declared (drain, delete, reset)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking")
	cmd.Flags().StringVar(&reviewed, "plan", "", "apply only if the plan still has this hash (from kubit plan)")
	return cmd
}

func lockHolder() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

func confirm(in io.Reader, out io.Writer) (bool, error) {
	if f, ok := in.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice == 0 {
			return false, errors.New("not a terminal; pass --yes to apply")
		}
	}
	fmt.Fprint(out, "Apply these changes? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

var planSymbol = map[string]string{cluster.ActCreate: "+", cluster.ActAdd: "+", cluster.ActRemove: "-"}

func printPlan(w io.Writer, p *cluster.Plan) {
	for _, pr := range p.Problems {
		fmt.Fprintf(w, "! %s\n", pr)
	}
	for _, c := range p.Changes {
		sym := planSymbol[c.Action]
		if sym == "" {
			sym = "~"
		}
		line := fmt.Sprintf("%s %-18s %s", sym, c.Action, c.Target)
		detail := c.Detail
		if c.Action == cluster.ActConfig {
			detail = ""
		}
		if detail != "" {
			if c.Target != "" {
				line += ": "
			}
			line += detail
		}
		if c.Blocked != "" {
			line += " (" + c.Blocked + ")"
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if c.Action == cluster.ActConfig {
			for _, l := range firstLines(c.Detail, 12) {
				fmt.Fprintf(w, "      %s\n", l)
			}
		}
	}
	for _, in := range p.Installs {
		fmt.Fprintf(w, "  install %s (%s at %s) erases %s\n", in.Hostname, in.Role, in.IP, in.Disk)
	}
	switch {
	case len(p.Problems) > 0:
		fmt.Fprintf(w, "%s cannot be applied.\n", p.Cluster)
	case p.Empty():
		fmt.Fprintf(w, "%s: no changes.\n", p.Cluster)
	default:
		fmt.Fprintf(w, "%s: %d change(s). Plan %s.\n", p.Cluster, len(p.Changes), p.Hash)
	}
}

func firstLines(s string, n int) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… %d more lines", len(lines)-n))
	}
	return lines
}
