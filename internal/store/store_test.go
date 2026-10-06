package store_test

import (
	"testing"

	"github.com/mikael/kubit/internal/store"
)

func TestMachineIdentityFollowsTheMACNotTheAddress(t *testing.T) {
	s := store.New()
	var changes int
	s.OnChange(func(store.Change) { changes++ })
	mac := "AA:BB:CC:00:00:01"
	s.UpsertNode(store.Machine{MAC: mac, IP: "10.0.0.5", State: "maintenance", Arch: "amd64"})
	s.UpsertNode(store.Machine{MAC: mac, IP: "10.0.0.5", State: "maintenance"})
	if changes != 1 {
		t.Errorf("an unchanged machine must not notify again: %d", changes)
	}
	s.UpsertNode(store.Machine{MAC: "aa:bb:cc:00:00:02", IP: "10.0.0.5", State: "maintenance"})
	m, err := s.GetMachine(mac)
	if err != nil || m.IP != "" || m.Arch != "amd64" {
		t.Errorf("an address taken by another MAC moves: %+v %v", m, err)
	}
	if got, err := s.GetNode("10.0.0.5"); err != nil || got.MAC != "aa:bb:cc:00:00:02" {
		t.Errorf("the address resolves to its new owner: %+v %v", got, err)
	}
}
