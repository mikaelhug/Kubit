package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mikael/kubit/internal/store"
	"github.com/mikael/kubit/internal/talos"
	"github.com/spf13/cobra"
)

func discoverCmd() *cobra.Command {
	var (
		timeout     time.Duration
		concurrency int
		asJSON      bool
		noSave      bool
	)
	cmd := &cobra.Command{
		Use:   "discover <cidr|ip>...",
		Short: "Find Talos nodes on the network and record maintenance-mode nodes as candidates",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addrs, err := talos.ExpandTargets(args)
			if err != nil {
				return err
			}
			results := talos.Scan(cmd.Context(), addrs, concurrency, timeout)
			if !noSave {
				s, err := openStore()
				if err != nil {
					return err
				}
				defer s.Close()
				for _, r := range results {
					if r.Err != nil {
						continue
					}
					row := store.NodeRow{IP: r.IP, Source: "scan", State: string(r.State)}
					if inv := r.Inventory; inv != nil {
						row.MAC = inv.PrimaryMAC()
						row.UUID, row.Serial = inv.UUID, inv.Serial
						row.Arch = inv.Arch
						row.TalosVersion = inv.TalosVersion
						row.Hardware, _ = json.Marshal(inv)
					}
					if err := s.UpsertNode(cmd.Context(), row); err != nil {
						return err
					}
				}
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(results)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "IP\tSTATE\tMAC\tARCH\tTALOS\tCPU\tRAM\tDISKS\tKVM")
			for _, r := range results {
				if r.Err != nil {
					fmt.Fprintf(tw, "%s\terror\t%v\n", r.IP, r.Err)
					continue
				}
				inv := r.Inventory
				if inv == nil {
					fmt.Fprintf(tw, "%s\t%s\t\t\t\t\t\t\t\n", r.IP, r.State)
					continue
				}
				var disks []string
				for _, d := range inv.InstallCandidates() {
					disks = append(disks, fmt.Sprintf("%s(%s)", d.DevPath, humanBytes(d.SizeBytes)))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%v\n",
					r.IP, r.State, inv.PrimaryMAC(), inv.Arch, inv.TalosVersion, inv.CPUs,
					humanBytes(inv.MemoryBytes), strings.Join(disks, ","), inv.KVM)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "per-host connect timeout")
	cmd.Flags().IntVar(&concurrency, "concurrency", 64, "parallel probes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&noSave, "no-save", false, "do not record results in the store")
	return cmd
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	if exp >= 2 && v < 100 {
		return fmt.Sprintf("%.1f%c", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.0f%c", v, "KMGTPE"[exp])
}
