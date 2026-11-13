package cluster

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/mikaelhug/kubit/internal/k8s"
	"github.com/mikaelhug/kubit/internal/talos"
)

const (
	wipeStep         = "wipe"
	longhornDiskStep = "longhorn disks"
	volumeTimeout    = 3 * time.Minute
)

type diskWipe struct {
	host, ip, path, volume, contents string
}

type longhornDisk struct {
	host, mount string
}

func (m *Manager) planDisks(ctx context.Context, d *Desired, p *Plan, ls *liveState, opts ConvergeOptions) {
	c := d.Cluster
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range c.Spec.Nodes {
		ln := ls.nodes[n.Hostname]
		if len(n.DataDisks) == 0 || ln == nil || !ln.member {
			continue
		}
		wg.Go(func() {
			wipes, problems := dataDiskState(ctx, n, ln.addr, d.Talosconfig)
			mu.Lock()
			defer mu.Unlock()
			p.Problems = append(p.Problems, problems...)
			for _, w := range wipes {
				ch := Change{Action: ActWipe, Target: n.Hostname, Detail: w.path + " (" + w.contents + ")"}
				if !opts.AllowWipe {
					ch.Blocked = "needs --allow-wipe"
				}
				p.wipes = append(p.wipes, w)
				p.Changes = append(p.Changes, ch)
			}
		})
	}
	wg.Wait()
	slices.SortFunc(p.wipes, func(a, b diskWipe) int { return strings.Compare(a.host+a.path, b.host+b.path) })
	if ls.api && c.Spec.Platform.Longhorn.Enabled {
		m.planLonghornDisks(ctx, d, p)
	}
}

func dataDiskState(ctx context.Context, n config.Node, ip string, talosconfig []byte) ([]diskWipe, []string) {
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tc, err := talos.Dial(call, ip, talosconfig)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: data disks: %v", n.Hostname, err)}
	}
	defer tc.Close()
	inv, err := tc.Storage(call)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: data disks: %v", n.Hostname, err)}
	}
	var wipes []diskWipe
	var problems []string
	for i, p := range n.DataDisks {
		disk, ok := inv.DiskByLink(p)
		_, used := inv.VolumeOn(disk.DevPath)
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: data disk %s is not attached", n.Hostname, p))
		case disk.DevPath == inv.SystemDisk:
			problems = append(problems, fmt.Sprintf("%s: data disk %s is the system disk", n.Hostname, p))
		case used:
		case disk.Format != "" || len(disk.Partitions) > 0:
			contents := strings.Join(slices.DeleteFunc(append([]string{disk.Format}, disk.Partitions...), func(s string) bool { return s == "" }), ", ")
			wipes = append(wipes, diskWipe{host: n.Hostname, ip: ip, path: p, volume: fmt.Sprintf("data-%d", i+1), contents: contents})
		}
	}
	return wipes, problems
}

func (m *Manager) planLonghornDisks(ctx context.Context, d *Desired, p *Plan) {
	c := d.Cluster
	kc, err := m.KubeClientFor(c.Metadata.Name, m.liveKubeconfig(d))
	if err != nil {
		return
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lh, err := kc.Longhorn(call)
	if err != nil || lh == nil {
		return
	}
	for _, n := range c.LonghornNodes() {
		i := slices.IndexFunc(lh.Nodes, func(ln k8s.LonghornNode) bool { return ln.Name == n.Hostname })
		if i < 0 {
			continue
		}
		for _, mount := range c.StorageMounts(n) {
			if slices.Contains(lh.Nodes[i].Disks, mount) {
				continue
			}
			p.longhornDisks = append(p.longhornDisks, longhornDisk{host: n.Hostname, mount: mount})
			p.Changes = append(p.Changes, Change{Action: ActLonghornDisk, Target: n.Hostname, Detail: mount})
		}
	}
}

func (m *Manager) wipeDisks(ctx context.Context, d *Desired, p *Plan, sink Sink) error {
	for _, w := range p.wipes {
		err := sink.Run(wipeStep, func() error {
			tc, err := talos.Dial(ctx, w.ip, d.Talosconfig)
			if err != nil {
				return err
			}
			defer tc.Close()
			inv, err := tc.Storage(ctx)
			if err != nil {
				return err
			}
			disk, ok := inv.DiskByLink(w.path)
			_, used := inv.VolumeOn(disk.DevPath)
			switch {
			case !ok:
				return fmt.Errorf("%s is not attached", w.path)
			case disk.DevPath == inv.SystemDisk:
				return fmt.Errorf("%s is the system disk", w.path)
			case used:
				return fmt.Errorf("%s holds a volume", w.path)
			}
			sink.Emit(Info, wipeStep, w.host, "wiping %s (%s)", w.path, disk.DevPath)
			return tc.WipeDisk(ctx, disk.DevPath)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", w.host, err)
		}
	}
	return nil
}

func (m *Manager) waitWiped(ctx context.Context, d *Desired, p *Plan, sink Sink) error {
	for _, w := range p.wipes {
		err := sink.Run(wipeStep, func() error {
			tc, err := talos.Dial(ctx, w.ip, d.Talosconfig)
			if err != nil {
				return err
			}
			defer tc.Close()
			if err := tc.WaitUserVolume(ctx, w.volume, volumeTimeout); err != nil {
				return err
			}
			sink.Emit(Info, wipeStep, w.host, "%s ready", w.volume)
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s: %w", w.host, err)
		}
	}
	return nil
}

func (m *Manager) addLonghornDisks(ctx context.Context, d *Desired, p *Plan, sink Sink) error {
	if len(p.longhornDisks) == 0 {
		return nil
	}
	c := d.Cluster
	return sink.Run(longhornDiskStep, func() error {
		kc, err := m.KubeClientFor(c.Metadata.Name, m.liveKubeconfig(d))
		if err != nil {
			return err
		}
		for _, ld := range p.longhornDisks {
			n := c.Spec.Nodes[c.NodeIndex(ld.host)]
			size, err := mountedBytes(ctx, n, ld.mount, d.Talosconfig)
			if err != nil {
				return fmt.Errorf("%s: %w", ld.host, err)
			}
			reserved := int64(size) * config.LonghornReservedPct / 100
			if err := kc.AddLonghornDisk(ctx, ld.host, path.Base(ld.mount), ld.mount, reserved); err != nil {
				return fmt.Errorf("%s: %w", ld.host, err)
			}
			sink.Emit(Info, longhornDiskStep, ld.host, "%s added", ld.mount)
		}
		return nil
	})
}

func mountedBytes(ctx context.Context, n config.Node, mount string, talosconfig []byte) (uint64, error) {
	tc, err := talos.Dial(ctx, n.TargetIP(), talosconfig)
	if err != nil {
		return 0, err
	}
	defer tc.Close()
	inv, err := tc.Storage(ctx)
	if err != nil {
		return 0, err
	}
	for _, v := range inv.Volumes {
		if v.Mount == mount && v.Phase == "ready" {
			return v.SizeBytes, nil
		}
	}
	return 0, fmt.Errorf("%s is not mounted", mount)
}
