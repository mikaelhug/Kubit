package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/spf13/cobra"
)

func nodeServicesCmd() *cobra.Command {
	var clusterName string
	cmd := &cobra.Command{
		Use:   "services <ip>",
		Short: "Show Talos service states on a cluster node",
		Args:  cobra.ExactArgs(1),
		RunE: withStore(func(cmd *cobra.Command, args []string, s *store.Store) error {
			tc, err := dialNode(cmd.Context(), s, clusterName, args[0])
			if err != nil {
				return err
			}
			defer tc.Close()
			services, err := tc.Services(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SERVICE\tSTATE\tHEALTHY\tLAST")
			for _, svc := range services {
				healthy := "?"
				last := ""
				if svc.Health != nil {
					healthy = fmt.Sprint(svc.Health.Healthy)
					last = svc.Health.LastMessage
				}
				if len(svc.Events.Events) > 0 {
					last = svc.Events.Events[len(svc.Events.Events)-1].Msg
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", svc.Id, svc.State, healthy, last)
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster the node belongs to")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func nodeLogsCmd() *cobra.Command {
	var clusterName, service string
	var tail int32
	cmd := &cobra.Command{
		Use:   "logs <ip>",
		Short: "Print dmesg (default) or a Talos service log from a cluster node",
		Args:  cobra.ExactArgs(1),
		RunE: withStore(func(cmd *cobra.Command, args []string, s *store.Store) error {
			tc, err := dialNode(cmd.Context(), s, clusterName, args[0])
			if err != nil {
				return err
			}
			defer tc.Close()
			var st dataStream
			if service == "" {
				st, err = tc.KernelLog(cmd.Context(), false)
			} else {
				st, err = tc.ServiceLog(cmd.Context(), service, false, tail)
			}
			if err != nil {
				return err
			}
			return drain(cmd.OutOrStdout(), st)
		}),
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster the node belongs to")
	cmd.Flags().StringVar(&service, "service", "", "Talos service id (kubelet, etcd, apid, ...); empty = dmesg")
	cmd.Flags().Int32Var(&tail, "tail", 200, "lines of service log")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func dialNode(ctx context.Context, s *store.Store, clusterName, ip string) (*talos.Client, error) {
	sec, err := s.GetClusterSecrets(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	return talos.Dial(ctx, ip, sec.Talosconfig)
}

type dataStream interface {
	Recv() (*common.Data, error)
}

func drain(w io.Writer, st dataStream) error {
	for {
		m, err := st.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(m.Bytes); err != nil {
			return err
		}
	}
}
