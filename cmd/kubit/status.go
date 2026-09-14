package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/spf13/cobra"
)

func statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status <cluster>",
		Short: "Live cluster overview: nodes, etcd, resources, platform",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := openManager()
			if err != nil {
				return err
			}
			defer m.Store.Close()
			st, err := m.Status(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			printStatus(cmd, st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func printStatus(cmd *cobra.Command, st *cluster.Status) {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "Cluster %s (%s)  Talos %s  Kubernetes %s  API %s reachable=%v\n",
		st.Name, st.State, st.TalosVersion, st.KubernetesVersion, st.Endpoint, st.APIReachable)
	t := st.Totals
	fmt.Fprintf(w, "CPU %s / %s   RAM %s / %s   Pods %d / %d   Nodes Ready %d/%d   etcd %d/%d healthy=%v leader=%s\n",
		milli(t.CPUMilli), milli(t.CPUCapMilli), humanBytes(uint64(t.MemBytes)), humanBytes(uint64(t.MemCapBytes)),
		t.Pods, t.PodCap, t.NodesReady, t.Nodes, st.Etcd.Members, st.Etcd.Expected, st.Etcd.Healthy, st.Etcd.Leader)
	if len(st.Etcd.Alarms) > 0 {
		fmt.Fprintf(w, "etcd alarms: %s\n", strings.Join(st.Etcd.Alarms, ", "))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HOSTNAME\tIP\tROLE\tREADY\tSTAGE\tTALOS\tKUBELET\tCPU\tRAM\tPODS\tGVISOR")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s/%s\t%s/%s\t%d\t%v\n",
			n.Hostname, n.IP, n.Role, ready, n.Stage, n.TalosVersion, n.KubeletVersion,
			milli(n.CPUMilli), milli(n.CPUCapMilli), humanBytes(uint64(n.MemBytes)), humanBytes(uint64(n.MemCapBytes)), n.Pods, n.GVisor)
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
