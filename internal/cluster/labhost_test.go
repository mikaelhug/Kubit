package cluster

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func TestLabDialByDriver(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{5}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st, dir)
	local := errors.New("local driver")
	m.Local = func() (labhost.Driver, error) { return nil, local }
	ctx := t.Context()
	mac := &store.Machine{MAC: "84:2f:57:45:7e:dc", IP: "192.168.105.1", LabHost: &store.LabHost{Driver: labhost.DriverVFKit}}
	if _, err := m.LabDial(ctx, mac); !errors.Is(err, local) {
		t.Errorf("a vfkit host must dial the local driver: %v", err)
	}
	if _, err := m.LabSSH(ctx, mac); err == nil {
		t.Error("SSH to this Mac must be refused")
	}
	if _, err := m.LabDial(ctx, &store.Machine{MAC: "x", LabHost: &store.LabHost{Driver: "hyperv"}}); err == nil {
		t.Error("an unknown driver must be refused")
	}
	if _, err := m.LabDial(ctx, &store.Machine{MAC: "x"}); err == nil {
		t.Error("a machine without a lab host record must be refused")
	}
}
