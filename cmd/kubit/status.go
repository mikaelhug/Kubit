package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/repo"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	var asJSON, watch bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "status [dir|cluster]",
		Short: "Live cluster overview: nodes, etcd, resources, platform",
		Args:  cobra.MaximumNArgs(1),
		RunE: withManager(func(cmd *cobra.Command, args []string, m *cluster.Manager) error {
			ctx := cmd.Context()
			name, err := statusTarget(cmd, m, dirArg(args))
			if err != nil {
				return err
			}
			show := func() error {
				st, err := m.Status(ctx, name)
				if err != nil {
					return err
				}
				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(st)
				}
				gvisor := false
				if c, _, err := m.LoadCluster(ctx, name); err == nil {
					gvisor = c.Spec.Platform.GVisor.Enabled
				}
				if watch {
					fmt.Fprint(cmd.OutOrStdout(), "\033[H\033[2J")
				}
				printStatus(cmd, st, gvisor)
				return nil
			}
			if err := show(); err != nil || !watch {
				return err
			}
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-t.C:
					if err := show(); err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), "status:", err)
					}
				}
			}
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "redraw until interrupted")
	cmd.Flags().DurationVar(&every, "interval", 5*time.Second, "redraw interval with --watch")
	return cmd
}

func statusTarget(cmd *cobra.Command, m *cluster.Manager, arg string) (string, error) {
	if _, err := os.Stat(filepath.Join(arg, repo.ClusterFile)); err != nil {
		if arg == "." {
			return "", errors.New("no cluster.yaml here; give a repo dir or a cluster name")
		}
		return arg, nil
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

func printStatus(cmd *cobra.Command, st *cluster.Status, gvisor bool) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Cluster %s (%s)  Talos %s  Kubernetes %s  API %s reachable=%v\n",
		st.Name, st.State, st.TalosVersion, st.KubernetesVersion, st.Endpoint, st.APIReachable)
	t := st.Totals
	fmt.Fprintf(w, "CPU %s / %s   RAM %s / %s   Pods %d / %d   Nodes Ready %d/%d   etcd %d/%d healthy=%v leader=%s\n",
		milli(t.CPUMilli), milli(t.CPUCapMilli), cluster.HumanBytes(uint64(t.MemBytes)), cluster.HumanBytes(uint64(t.MemCapBytes)),
		t.Pods, t.PodCap, t.NodesReady, t.Nodes, st.Etcd.Members, st.Etcd.Expected, st.Etcd.Healthy, st.Etcd.Leader)
	if len(st.Etcd.Alarms) > 0 {
		fmt.Fprintf(w, "etcd alarms: %s\n", strings.Join(st.Etcd.Alarms, ", "))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "HOSTNAME\tIP\tROLE\tREADY\tSTAGE\tTALOS\tKUBELET\tCPU\tRAM\tPODS"
	if gvisor {
		header += "\tGVISOR"
	}
	fmt.Fprintln(tw, header)
	if st.APIError != "" {
		fmt.Fprintf(w, "Kubernetes API error: %s\n", st.APIError)
	}
	for _, n := range st.Nodes {
		ready := "NotReady"
		switch {
		case !st.APIReachable:
			ready = "k8s-unknown"
		case !n.Registered:
			ready = "not-registered"
		case n.Ready:
			ready = "Ready"
		}
		if n.Unschedulable {
			ready += ",cordoned"
		}
		if !n.TalosReachable {
			n.Stage = "unreachable: " + n.TalosError
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s/%s\t%s/%s\t%d",
			n.Hostname, n.IP, n.Role, ready, n.Stage, n.TalosVersion, n.KubeletVersion,
			milli(n.CPUMilli), milli(n.CPUCapMilli), cluster.HumanBytes(uint64(n.MemBytes)), cluster.HumanBytes(uint64(n.MemCapBytes)), n.Pods)
		if gvisor {
			fmt.Fprintf(tw, "\t%v", n.GVisor)
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
	if st.Platform != nil {
		if st.Platform.Error != "" {
			fmt.Fprintf(w, "Platform: error: %s\n", st.Platform.Error)
		} else {
			fmt.Fprintf(w, "Platform: applied %s", st.Platform.AppliedAt)
			for k, v := range st.Platform.Outputs {
				if v != "" {
					fmt.Fprintf(w, "  %s=%s", k, v)
				}
			}
			fmt.Fprintln(w)
		}
	}
}

func milli(m int64) string {
	if m >= 1000 {
		return fmt.Sprintf("%.1f", float64(m)/1000)
	}
	return fmt.Sprintf("%dm", m)
}
