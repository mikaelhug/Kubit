package k8s

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StorageClass struct {
	Name        string `json:"name"`
	Provisioner string `json:"provisioner"`
	Default     bool   `json:"default"`
	Reclaim     string `json:"reclaim"`
	Binding     string `json:"binding"`
	Expandable  bool   `json:"expandable"`
}

type Volume struct {
	Name       string `json:"name"`
	Capacity   int64  `json:"capacityBytes"`
	Phase      string `json:"phase"`
	Class      string `json:"class"`
	Claim      string `json:"claim,omitempty"`
	AccessMode string `json:"accessModes"`
	Reclaim    string `json:"reclaim"`
	Age        string `json:"age"`
}

type Claim struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Requested int64  `json:"requestedBytes"`
	Capacity  int64  `json:"capacityBytes"`
	Class     string `json:"class"`
	Volume    string `json:"volume,omitempty"`
	Age       string `json:"age"`
	AgeSec    int64  `json:"ageSec"`
}

type Storage struct {
	Classes []StorageClass `json:"classes"`
	Volumes []Volume       `json:"volumes"`
	Claims  []Claim        `json:"claims"`
}

func (c *Client) Storage(ctx context.Context) (*Storage, error) {
	out := &Storage{Classes: []StorageClass{}, Volumes: []Volume{}, Claims: []Claim{}}
	age := func(t metav1.Time) string { return metav1.Now().Sub(t.Time).Truncate(1e9).String() }
	scs, err := c.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, sc := range scs.Items {
		s := StorageClass{Name: sc.Name, Provisioner: sc.Provisioner, Default: sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true"}
		if sc.ReclaimPolicy != nil {
			s.Reclaim = string(*sc.ReclaimPolicy)
		}
		if sc.VolumeBindingMode != nil {
			s.Binding = string(*sc.VolumeBindingMode)
		}
		if sc.AllowVolumeExpansion != nil {
			s.Expandable = *sc.AllowVolumeExpansion
		}
		out.Classes = append(out.Classes, s)
	}
	pvs, err := c.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, pv := range pvs.Items {
		v := Volume{Name: pv.Name, Capacity: pv.Spec.Capacity.Storage().Value(), Phase: string(pv.Status.Phase), Class: pv.Spec.StorageClassName, Reclaim: string(pv.Spec.PersistentVolumeReclaimPolicy), Age: age(pv.CreationTimestamp)}
		if pv.Spec.ClaimRef != nil {
			v.Claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
		}
		for _, m := range pv.Spec.AccessModes {
			v.AccessMode += string(m) + " "
		}
		out.Volumes = append(out.Volumes, v)
	}
	pvcs, err := c.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, pvc := range pvcs.Items {
		cl := Claim{Namespace: pvc.Namespace, Name: pvc.Name, Phase: string(pvc.Status.Phase), Requested: pvc.Spec.Resources.Requests.Storage().Value(), Capacity: pvc.Status.Capacity.Storage().Value(), Volume: pvc.Spec.VolumeName, Age: age(pvc.CreationTimestamp), AgeSec: int64(metav1.Now().Sub(pvc.CreationTimestamp.Time).Seconds())}
		if pvc.Spec.StorageClassName != nil {
			cl.Class = *pvc.Spec.StorageClassName
		}
		out.Claims = append(out.Claims, cl)
	}
	sort.Slice(out.Claims, func(i, j int) bool {
		return out.Claims[i].Namespace+"/"+out.Claims[i].Name < out.Claims[j].Namespace+"/"+out.Claims[j].Name
	})
	return out, nil
}
