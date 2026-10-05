package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

type vmPlacement struct{ System, Data labhost.Placement }

type poolSpec struct{ Name, Device, Disk string }

type storageLayout struct {
	VMs      []vmPlacement
	Create   []poolSpec
	Release  []string
	Warnings []string
}

func planStorage(sizes []vmSize, capa labhost.Capacity, vms []labhost.VM) (storageLayout, error) {
	var out storageLayout
	disks := map[string]labhost.HostDisk{}
	for _, d := range capa.Disks {
		disks[d.Key] = d
	}
	pools := map[string]labhost.Pool{}
	poolOf := map[string]string{}
	sizeOf := map[string]int64{}
	names := []labhost.Pool{{Name: labhost.SystemPool}}
	for _, p := range capa.Pools {
		pools[p.Name] = p
		sizeOf[p.Name] = p.SizeBytes
		if p.Name == labhost.SystemPool && p.Dir != "" {
			sizeOf[p.Name] = max(0, p.SizeBytes*95/100-8<<30)
		}
		names = append(names, p)
		if p.Disk != "" {
			poolOf[p.Disk] = p.Name
		}
	}
	committed := map[string]int64{}
	owner := map[string]string{}
	for _, vm := range vms {
		for _, d := range vm.Disks {
			if d.Device != "" {
				owner[d.Device] = vm.Name
			} else {
				committed[orSystem(d.Pool)] += int64(d.GiB) << 30
			}
		}
	}
	defaultPool, best := labhost.SystemPool, int64(-1)
	for _, p := range capa.Pools {
		if p.Name != labhost.SystemPool && p.Mounted && p.FreeBytes > best {
			defaultPool, best = p.Name, p.FreeBytes
		}
	}
	pending := map[string]string{}
	whole := map[string]bool{}
	imaged := map[string]bool{}
	var touched []string
	resolve := func(choice, fallback string) (labhost.Placement, error) {
		isWhole, key, err := labhost.ParseDiskChoice(choice)
		if err != nil {
			return labhost.Placement{}, err
		}
		if key == "" {
			return labhost.Placement{Pool: fallback}, nil
		}
		if key == labhost.DebianDisk {
			if isWhole {
				return labhost.Placement{}, errors.New("Debian's disk cannot be used whole")
			}
			return labhost.Placement{Pool: labhost.SystemPool}, nil
		}
		d, ok := disks[key]
		if !ok {
			return labhost.Placement{}, fmt.Errorf("disk %s is not on this host", key)
		}
		switch {
		case d.Use == labhost.DiskBusy:
			return labhost.Placement{}, fmt.Errorf("%s is in use on the host", d.Label())
		case isWhole && d.Use == labhost.DiskOS:
			return labhost.Placement{}, fmt.Errorf("%s holds Debian", d.Label())
		case isWhole && whole[key]:
			return labhost.Placement{}, fmt.Errorf("%s is chosen twice", d.Label())
		case isWhole && imaged[key], !isWhole && whole[key]:
			return labhost.Placement{}, fmt.Errorf("%s cannot hold images and be used whole", d.Label())
		case isWhole && owner[d.ID] != "":
			return labhost.Placement{}, fmt.Errorf("%s belongs to %s", d.Label(), owner[d.ID])
		}
		if isWhole {
			if p := poolOf[key]; p != "" {
				if committed[p] > 0 {
					return labhost.Placement{}, fmt.Errorf("%s holds VM images", d.Label())
				}
				if !slices.Contains(out.Release, p) {
					out.Release = append(out.Release, p)
				}
			}
			whole[key] = true
			return labhost.Placement{Device: d.ID}, nil
		}
		imaged[key] = true
		switch d.Use {
		case labhost.DiskOS:
			return labhost.Placement{Pool: labhost.SystemPool}, nil
		case labhost.DiskPool:
			p := pools[poolOf[key]]
			if !p.Mounted {
				return labhost.Placement{}, fmt.Errorf("%s is not mounted on the host", d.Label())
			}
			return labhost.Placement{Pool: p.Name}, nil
		}
		if name, ok := pending[key]; ok {
			return labhost.Placement{Pool: name}, nil
		}
		name := labhost.NextPoolName(names)
		names = append(names, labhost.Pool{Name: name})
		pending[key] = name
		sizeOf[name] = int64(d.SizeBytes)
		out.Create = append(out.Create, poolSpec{Name: name, Device: d.ID, Disk: key})
		return labhost.Placement{Pool: name}, nil
	}
	add := func(pool string, gib int) {
		committed[pool] += int64(gib) << 30
		if !slices.Contains(touched, pool) {
			touched = append(touched, pool)
		}
	}
	for i, v := range sizes {
		sys, err := resolve(v.SystemDisk, defaultPool)
		if err != nil {
			return out, fmt.Errorf("vm %d system disk: %w", i+1, err)
		}
		var data labhost.Placement
		switch {
		case v.DataDisk != "":
			if data, err = resolve(v.DataDisk, defaultPool); err != nil {
				return out, fmt.Errorf("vm %d data disk: %w", i+1, err)
			}
		case v.DataGiB > 0:
			data.Pool = sys.Pool
			if sys.Device != "" {
				data.Pool = defaultPool
			}
		}
		if sys.Device == "" {
			add(sys.Pool, v.DiskGiB)
		}
		if data.Pool != "" {
			add(data.Pool, v.DataGiB)
		}
		out.VMs = append(out.VMs, vmPlacement{System: sys, Data: data})
	}
	for _, p := range touched {
		if size := sizeOf[p]; size > 0 && committed[p] > size {
			out.Warnings = append(out.Warnings, fmt.Sprintf("Images on %s may grow to %d GiB; the disk has %d GiB.", poolLabel(p, capa, out.Create), committed[p]>>30, size>>30))
		}
	}
	return out, nil
}

func orSystem(pool string) string {
	if pool == "" {
		return labhost.SystemPool
	}
	return pool
}

func poolLabel(name string, capa labhost.Capacity, planned []poolSpec) string {
	if name == labhost.SystemPool {
		return "Debian's disk"
	}
	key := ""
	for _, p := range capa.Pools {
		if p.Name == name {
			key = p.Disk
		}
	}
	for _, p := range planned {
		if p.Name == name {
			key = p.Disk
		}
	}
	for _, d := range capa.Disks {
		if d.Key == key {
			return d.Name()
		}
	}
	return name
}

func inventoryCapacity(install *labhost.DiskRef, refs []labhost.DiskRef) labhost.Capacity {
	var capa labhost.Capacity
	var size int64
	for _, r := range refs {
		use := labhost.DiskFree
		if install != nil && r.Key == install.Key {
			use, size = labhost.DiskOS, max(0, int64(r.SizeBytes)*93/100-12<<30)
		}
		capa.Disks = append(capa.Disks, labhost.HostDisk{DiskRef: r, ID: r.Key, Use: use})
	}
	capa.Pools = []labhost.Pool{{Name: labhost.SystemPool, Mounted: true, SizeBytes: size, FreeBytes: size}}
	return capa
}

func translatePlan(req addVMsRequest, planned []labhost.DiskRef, host []labhost.HostDisk) (addVMsRequest, error) {
	byKey := map[string]labhost.DiskRef{}
	for _, r := range planned {
		byKey[r.Key] = r
	}
	rewrite := func(choice string) (string, error) {
		isWhole, key, err := labhost.ParseDiskChoice(choice)
		if err != nil || key == "" || key == labhost.DebianDisk {
			return choice, err
		}
		ref, ok := byKey[key]
		if !ok {
			return "", fmt.Errorf("disk %s is not in the plan", key)
		}
		d, ok := labhost.MatchDisk(ref, host)
		if !ok {
			return "", fmt.Errorf("the planned disk %s was not found on the host", ref.Label())
		}
		if d.Use == labhost.DiskOS {
			if isWhole {
				return "", fmt.Errorf("the planned disk %s holds Debian", ref.Label())
			}
			return "image:" + labhost.DebianDisk, nil
		}
		if isWhole {
			return "whole:" + d.Key, nil
		}
		return "image:" + d.Key, nil
	}
	out := req
	out.Each = slices.Clone(req.Each)
	for i := range out.Each {
		var err error
		if out.Each[i].SystemDisk, err = rewrite(out.Each[i].SystemDisk); err != nil {
			return out, err
		}
		if out.Each[i].DataDisk, err = rewrite(out.Each[i].DataDisk); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Server) applyStorage(ctx context.Context, lc labhost.Driver, mac string, sizes []vmSize, vms []labhost.VM, sink func(format string, args ...any)) (storageLayout, error) {
	st, ok := lc.(labhost.Storage)
	if !ok {
		return storageLayout{}, nil
	}
	capa, err := lc.Capacity(labhost.FreshCapacity(ctx))
	if err != nil {
		return storageLayout{}, err
	}
	layout, err := planStorage(sizes, capa, vms)
	if err != nil {
		return layout, err
	}
	for _, w := range layout.Warnings {
		sink("%s", w)
	}
	for _, name := range layout.Release {
		sink("releasing %s for a whole-disk VM", name)
		if err := st.RemovePool(ctx, name); err != nil {
			return layout, err
		}
	}
	for _, p := range layout.Create {
		sink("formatting %s for VM images (%s)", poolLabel(p.Name, capa, layout.Create), p.Name)
		if err := st.CreatePool(ctx, p.Name, p.Device); err != nil {
			return layout, err
		}
	}
	if len(layout.Create)+len(layout.Release) > 0 {
		if capa, err = lc.Capacity(labhost.FreshCapacity(ctx)); err == nil {
			_ = s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.Capacity = capa })
		}
	}
	return layout, nil
}

func (s *Server) handleLabPoolRelease(w http.ResponseWriter, r *http.Request) {
	mac, name := pathMAC(r), r.PathValue("name")
	if name == labhost.SystemPool || !labhost.ValidPoolName(name) {
		writeErr(w, badRequest("unknown storage disk"))
		return
	}
	unlock, ok := s.holdLock(w, r, labBusy, "labhost:"+mac)
	if !ok {
		return
	}
	defer unlock()
	host, ok := s.labHostOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	lc, err := s.manager.LabDial(ctx, host)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer lc.Close()
	st, ok := lc.(labhost.Storage)
	if !ok {
		writeErr(w, conflict("This lab host has no storage disks."))
		return
	}
	capa, err := lc.Capacity(labhost.FreshCapacity(ctx))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !slices.ContainsFunc(capa.Pools, func(p labhost.Pool) bool { return p.Name == name }) {
		writeErr(w, &statusError{Status: http.StatusNotFound, Msg: "unknown storage disk"})
		return
	}
	vms, err := lc.List(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, vm := range vms {
		if slices.ContainsFunc(vm.Disks, func(d labhost.VMDisk) bool { return d.Pool == name }) {
			writeErr(w, conflict(fmt.Sprintf("%s holds images of %s; delete or move that VM first.", poolLabel(name, capa, nil), vm.Name)))
			return
		}
	}
	if err := st.RemovePool(ctx, name); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.store.ResolveEvents(ctx, store.LabHostKey(mac), name, "labhost.disk-low")
	if fresh, err := lc.Capacity(labhost.FreshCapacity(ctx)); err == nil {
		capa = fresh
	}
	_ = s.store.UpdateLabHost(ctx, mac, func(l *store.LabHost) { l.Capacity, l.VMs = capa, vms })
	_ = s.store.Audit(ctx, "", "labhost.pool.release", mac+" "+name)
	w.WriteHeader(http.StatusNoContent)
}
