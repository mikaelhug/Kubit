package k8s

import (
	"context"
	"sort"
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
	scs, err := c.storageClasses(ctx)
	if err != nil {
		return nil, err
	}
	for _, sc := range scs {
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
	pvs, err := c.volumes(ctx)
	if err != nil {
		return nil, err
	}
	for _, pv := range pvs {
		v := Volume{Name: pv.Name, Capacity: pv.Spec.Capacity.Storage().Value(), Phase: string(pv.Status.Phase), Class: pv.Spec.StorageClassName, Reclaim: string(pv.Spec.PersistentVolumeReclaimPolicy)}
		v.Age, _ = age(pv.CreationTimestamp)
		if pv.Spec.ClaimRef != nil {
			v.Claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
		}
		for _, m := range pv.Spec.AccessModes {
			v.AccessMode += string(m) + " "
		}
		out.Volumes = append(out.Volumes, v)
	}
	if out.Claims, err = c.Claims(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Claims(ctx context.Context) ([]Claim, error) {
	pvcs, err := c.claims(ctx)
	if err != nil {
		return nil, err
	}
	out := []Claim{}
	for _, pvc := range pvcs {
		cl := Claim{Namespace: pvc.Namespace, Name: pvc.Name, Phase: string(pvc.Status.Phase), Requested: pvc.Spec.Resources.Requests.Storage().Value(), Capacity: pvc.Status.Capacity.Storage().Value(), Volume: pvc.Spec.VolumeName}
		cl.Age, cl.AgeSec = age(pvc.CreationTimestamp)
		if pvc.Spec.StorageClassName != nil {
			cl.Class = *pvc.Spec.StorageClassName
		}
		out = append(out, cl)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name
	})
	return out, nil
}
