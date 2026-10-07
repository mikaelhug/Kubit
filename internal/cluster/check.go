package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mikaelhug/kubit/internal/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

type metalMode struct{}

func (metalMode) String() string        { return "metal" }
func (metalMode) RequiresInstall() bool { return true }
func (metalMode) InContainer() bool     { return false }

func CheckDeclaration(c *config.Cluster, bundle *secrets.Bundle) error {
	gen, err := config.Generate(c, bundle, "ghcr.io/siderolabs/installer:"+c.Spec.TalosVersion)
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
