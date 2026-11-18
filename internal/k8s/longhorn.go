package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

const longhornNamespace = "longhorn-system"

var (
	longhornNodeGVR    = schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "nodes"}
	longhornVolumeGVR  = schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "volumes"}
	longhornSettingGVR = schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "settings"}
)

const (
	longhornOverProvisioning = "storage-over-provisioning-percentage"
	longhornProvisioner      = "driver.longhorn.io"
)

type LonghornNode struct {
	Name        string   `json:"name"`
	Ready       bool     `json:"ready"`
	Schedulable bool     `json:"schedulable"`
	Reason      string   `json:"reason,omitempty"`
	Disks       []string `json:"disks"`
	Maximum     int64    `json:"maximumBytes"`
	Reserved    int64    `json:"reservedBytes"`
	Available   int64    `json:"availableBytes"`
	Scheduled   int64    `json:"scheduledBytes"`
	Replicas    int      `json:"replicas"`
	Capacity    int64    `json:"-"`
	Headroom    int64    `json:"-"`
}

type LonghornVolume struct {
	PV         string `json:"-"`
	Size       int64  `json:"sizeBytes"`
	Actual     int64  `json:"actualBytes"`
	Replicas   int    `json:"replicas"`
	Robustness string `json:"robustness"`
	State      string `json:"state"`
}

type Longhorn struct {
	Nodes    []LonghornNode
	Volumes  []LonghornVolume
	Replicas int
}

func (c *Client) Longhorn(ctx context.Context) (*Longhorn, error) {
	nodes, err := c.crdObjects(ctx, longhornNodeGVR, longhornNamespace)
	if err != nil || nodes == nil {
		return nil, err
	}
	settings, err := c.crdObjects(ctx, longhornSettingGVR, longhornNamespace)
	if err != nil {
		return nil, err
	}
	lh := &Longhorn{}
	if scs, err := c.storageClasses(ctx); err == nil {
		for _, sc := range scs {
			def := sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true"
			if n, err := strconv.Atoi(sc.Parameters["numberOfReplicas"]); err == nil && sc.Provisioner == longhornProvisioner && (def || lh.Replicas == 0) {
				lh.Replicas = n
			}
		}
	}
	overProvisioning := int64(100)
	for _, s := range settings {
		v, _, _ := unstructured.NestedString(s.Object, "value")
		if pct, err := strconv.ParseInt(v, 10, 64); err == nil && pct > 0 && s.GetName() == longhornOverProvisioning {
			overProvisioning = pct
		}
	}
	lh.Nodes = make([]LonghornNode, 0, len(nodes))
	for _, u := range nodes {
		lh.Nodes = append(lh.Nodes, longhornNodeOf(u, overProvisioning))
	}
	sort.Slice(lh.Nodes, func(i, j int) bool { return lh.Nodes[i].Name < lh.Nodes[j].Name })
	list, err := c.crdObjects(ctx, longhornVolumeGVR, longhornNamespace)
	if err != nil {
		return nil, err
	}
	for _, u := range list {
		lh.Volumes = append(lh.Volumes, longhornVolumeOf(u))
	}
	return lh, nil
}

func longhornVolumeOf(u unstructured.Unstructured) LonghornVolume {
	n, _, _ := unstructured.NestedInt64(u.Object, "spec", "numberOfReplicas")
	size, _, _ := unstructured.NestedString(u.Object, "spec", "size")
	actual, _, _ := unstructured.NestedFieldNoCopy(u.Object, "status", "actualSize")
	robust, _, _ := unstructured.NestedString(u.Object, "status", "robustness")
	state, _, _ := unstructured.NestedString(u.Object, "status", "state")
	pv, _, _ := unstructured.NestedString(u.Object, "status", "kubernetesStatus", "pvName")
	if pv == "" {
		pv = u.GetName()
	}
	v := LonghornVolume{PV: pv, Actual: int64Of(actual), Replicas: int(n), Robustness: robust, State: state}
	v.Size, _ = strconv.ParseInt(size, 10, 64)
	return v
}

func longhornNodeOf(u unstructured.Unstructured, overProvisioning int64) LonghornNode {
	n := LonghornNode{Name: u.GetName(), Disks: []string{}}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	ready, notReady := condition(conds, "Ready")
	schedulable, notSchedulable := condition(conds, "Schedulable")
	allowed, _, _ := unstructured.NestedBool(u.Object, "spec", "allowScheduling")
	counted := ready && schedulable && allowed
	n.Ready, n.Schedulable = ready, schedulable
	switch {
	case !ready:
		n.Reason = notReady
	case !schedulable:
		n.Reason = notSchedulable
	}
	specs, _, _ := unstructured.NestedMap(u.Object, "spec", "disks")
	statuses, _, _ := unstructured.NestedMap(u.Object, "status", "diskStatus")
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec, _ := specs[name].(map[string]any)
		path, _ := spec["path"].(string)
		n.Disks = append(n.Disks, path)
		st, _ := statuses[name].(map[string]any)
		if st == nil {
			continue
		}
		maximum, reserved, scheduled := int64Of(st["storageMaximum"]), int64Of(spec["storageReserved"]), int64Of(st["storageScheduled"])
		n.Maximum += maximum
		n.Available += int64Of(st["storageAvailable"])
		n.Scheduled += scheduled
		n.Reserved += reserved
		replicas, _ := st["scheduledReplica"].(map[string]any)
		n.Replicas += len(replicas)
		diskConds, _ := st["conditions"].([]any)
		diskOK, _ := condition(diskConds, "Schedulable")
		diskAllowed, _ := spec["allowScheduling"].(bool)
		evicting, _ := spec["evictionRequested"].(bool)
		if !counted || !diskOK || !diskAllowed || evicting || maximum <= reserved {
			continue
		}
		n.Capacity += maximum - reserved
		n.Headroom += max(0, (maximum-reserved)*overProvisioning/100-scheduled)
	}
	return n
}

func condition(conds []any, kind string) (bool, string) {
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] != kind {
			continue
		}
		reason, _ := m["reason"].(string)
		return m["status"] == "True", reason
	}
	return false, ""
}

func int64Of(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func (c *Client) AddLonghornDisk(ctx context.Context, node, name, path string, reservedBytes int64) error {
	dyn, err := c.dynClient()
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"disks": map[string]any{name: map[string]any{
		"path": path, "allowScheduling": true, "storageReserved": reservedBytes, "diskType": "filesystem", "evictionRequested": false, "tags": []string{},
	}}}})
	if err != nil {
		return err
	}
	_, err = dyn.Resource(longhornNodeGVR).Namespace(longhornNamespace).Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (c *Client) SetLonghornEviction(ctx context.Context, node string, evict bool) (bool, error) {
	dyn, err := c.dynClient()
	if err != nil {
		return false, err
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"allowScheduling": !evict, "evictionRequested": evict}})
	if err != nil {
		return false, err
	}
	_, err = dyn.Resource(longhornNodeGVR).Namespace(longhornNamespace).Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) DeleteLonghornNode(ctx context.Context, node string) error {
	dyn, err := c.dynClient()
	if err != nil {
		return err
	}
	err = dyn.Resource(longhornNodeGVR).Namespace(longhornNamespace).Delete(ctx, node, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) WaitLonghornEmpty(ctx context.Context, nodes []string, stall time.Duration, progress func(left int)) error {
	dyn, err := c.dynClient()
	if err != nil {
		return err
	}
	res := dyn.Resource(longhornNodeGVR).Namespace(longhornNamespace)
	left := map[string]int{}
	total := func() int {
		sum := 0
		for _, r := range left {
			sum += r
		}
		return sum
	}
	lowest, since := -1, time.Now()
	moved := func() bool {
		t := total()
		if lowest >= 0 && t >= lowest {
			return false
		}
		lowest, since = t, time.Now()
		if progress != nil {
			progress(t)
		}
		return true
	}
	for {
		list, err := res.List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, n := range nodes {
			left[n] = 0
		}
		for _, u := range list.Items {
			if _, ok := left[u.GetName()]; ok {
				left[u.GetName()] = longhornNodeOf(u, 100).Replicas
			}
		}
		moved()
		if total() == 0 {
			return nil
		}
		w, err := res.Watch(ctx, metav1.ListOptions{ResourceVersion: list.GetResourceVersion()})
		if err != nil {
			return err
		}
		if err := func() error {
			defer w.Stop()
			timer := time.NewTimer(time.Until(since.Add(stall)))
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return fmt.Errorf("no Longhorn replica moved in %s; %d remain", stall, total())
				case ev, ok := <-w.ResultChan():
					if !ok {
						return nil
					}
					u, isObj := ev.Object.(*unstructured.Unstructured)
					if !isObj {
						continue
					}
					if _, want := left[u.GetName()]; !want {
						continue
					}
					left[u.GetName()] = longhornNodeOf(*u, 100).Replicas
					if ev.Type == watch.Deleted {
						left[u.GetName()] = 0
					}
					if moved() {
						timer.Reset(time.Until(since.Add(stall)))
					}
					if total() == 0 {
						return nil
					}
				}
			}
		}(); err != nil || total() == 0 {
			return err
		}
	}
}
