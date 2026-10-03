package cluster

import (
	"bytes"
	"context"
	"errors"

	"github.com/mikael/kubit/internal/store"
)

type ConfigStatus struct {
	Behind []string `json:"behind"`
}

func (m *Manager) ConfigStatus(ctx context.Context, name string) (ConfigStatus, error) {
	st := ConfigStatus{Behind: []string{}}
	c, row, err := m.LoadCluster(ctx, name)
	if err != nil {
		return st, err
	}
	if !Observable(row.State) {
		return st, nil
	}
	_, gen, err := m.generateNodeConfigs(ctx, c)
	if errors.Is(err, store.ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	for _, n := range c.Spec.Nodes {
		applied, err := m.Store.GetNodeMachineConfig(ctx, n.IP)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return st, err
		}
		if !bytes.Equal(applied, gen.Nodes[n.Hostname]) {
			st.Behind = append(st.Behind, n.Hostname)
		}
	}
	return st, nil
}
