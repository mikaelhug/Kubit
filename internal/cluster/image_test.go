package cluster

import (
	"testing"

	"github.com/mikael/kubit/internal/config"
)

func TestImageOutdated(t *testing.T) {
	c := &config.Cluster{}
	c.Spec.SchematicID = "aaa"
	c.Spec.Pools = []config.Pool{{Name: "worker"}, {Name: "gpu", Extensions: []string{"x"}, SchematicID: "ggg"}}
	if imageOutdated(c, "aaa", map[string]string{"gpu": "ggg"}) {
		t.Error("same schematics are not outdated")
	}
	if !imageOutdated(c, "bbb", map[string]string{"gpu": "ggg"}) {
		t.Error("a changed cluster schematic is outdated")
	}
	if !imageOutdated(c, "aaa", map[string]string{"gpu": "hhh"}) {
		t.Error("a changed pool schematic is outdated")
	}
}
