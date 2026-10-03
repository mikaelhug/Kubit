package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/store"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

func (m *Manager) CheckCluster(ctx context.Context, declared *config.Cluster) error {
	b, err := declared.Marshal()
	if err != nil {
		return err
	}
	c, err := config.Parse(b)
	if err != nil {
		return err
	}
	_, bundle, err := m.loadSecrets(ctx, c.Metadata.Name)
	if errors.Is(err, store.ErrNotFound) {
		bundle, err = trialBundle(c.Spec.TalosVersion)
	}
	if err != nil {
		return err
	}
	gen, err := config.Generate(c, bundle, m.installer(c))
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range orderedNodes(c) {
		cfg, err := configloader.NewFromBytes(gen.Nodes[n.Hostname])
		if err == nil {
			_, err = cfg.Validate(metalMode{})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s (%s): %s", n.Hostname, n.Role, flatten(err)))
		}
	}
	return errors.Join(errs...)
}

func flatten(err error) string {
	multi, ok := err.(interface{ WrappedErrors() []error })
	if !ok {
		return err.Error()
	}
	msgs := make([]string, 0, len(multi.WrappedErrors()))
	for _, e := range multi.WrappedErrors() {
		msgs = append(msgs, e.Error())
	}
	return strings.Join(msgs, "; ")
}

var trialBundles sync.Map

func trialBundle(talosVersion string) (*secrets.Bundle, error) {
	once, _ := trialBundles.LoadOrStore(talosVersion, sync.OnceValues(func() (*secrets.Bundle, error) {
		contract, err := talosconfig.ParseContractFromVersion(talosVersion)
		if err != nil {
			return nil, err
		}
		return secrets.NewBundle(secrets.NewClock(), contract)
	}))
	return once.(func() (*secrets.Bundle, error))()
}
