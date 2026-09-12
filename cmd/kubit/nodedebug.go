package main

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/mikael/kubit/internal/talos"
	"github.com/spf13/cobra"
)

func nodeServicesCmd() *cobra.Command {
	var clusterName string
	cmd := &cobra.Command{
		Use:   "services <ip>",
		Short: "Show Talos service states on a cluster node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			sec, err := s.GetClusterSecrets(cmd.Context(), clusterName)
			if err != nil {
				return err
			}
			tc, err := talos.Dial(cmd.Context(), args[0], sec.Talosconfig)
			if err != nil {
				return err
			}
			defer tc.Close()
			resp, err := tc.ServiceList(tc.Context(cmd.Context()))
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SERVICE\tSTATE\tHEALTHY\tLAST")
			for _, m := range resp.Messages {
				for _, svc := range m.Services {
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
			}
			return tw.Flush()
		},
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
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			defer s.Close()
			sec, err := s.GetClusterSecrets(cmd.Context(), clusterName)
			if err != nil {
				return err
			}
			tc, err := talos.Dial(cmd.Context(), args[0], sec.Talosconfig)
			if err != nil {
				return err
			}
			defer tc.Close()
			var stream interface{ Recv() (*talosLogMsg, error) }
			_ = stream
			if service == "" {
				st, err := tc.Dmesg(tc.Context(cmd.Context()), false, false)
				if err != nil {
					return err
				}
				return drain(cmd.OutOrStdout(), func() ([]byte, error) {
					m, err := st.Recv()
					if err != nil {
						return nil, err
					}
					return m.Bytes, nil
				})
			}
			st, err := tc.Logs(tc.Context(cmd.Context()), "system", 0, service, false, tail)
			if err != nil {
				return err
			}
			return drain(cmd.OutOrStdout(), func() ([]byte, error) {
				m, err := st.Recv()
				if err != nil {
					return nil, err
				}
				return m.Bytes, nil
			})
		},
	}
	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster the node belongs to")
	cmd.Flags().StringVar(&service, "service", "", "Talos service id (kubelet, etcd, apid, ...); empty = dmesg")
	cmd.Flags().Int32Var(&tail, "tail", 200, "lines of service log")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

type talosLogMsg struct{ Bytes []byte }

func drain(w io.Writer, recv func() ([]byte, error)) error {
	for {
		b, err := recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		w.Write(b)
	}
}
