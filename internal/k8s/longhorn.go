package k8s

import (
	"context"
	"sort"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
