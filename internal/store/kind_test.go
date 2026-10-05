package store

import "testing"

func TestMachineKind(t *testing.T) {
	cases := []struct {
		name  string
		m     Machine
		want  Kind
		talos bool
	}{
		{"member", Machine{Cluster: "lab", State: "ready"}, KindMember, true},
		{"installing member", Machine{Cluster: "lab", State: "installing"}, KindMember, true},
		{"maintenance", Machine{State: "maintenance"}, KindMaintenance, true},
		{"configured", Machine{State: "configured"}, KindConfigured, false},
		{"offline", Machine{State: "offline"}, KindOffline, false},
		{"empty", Machine{}, KindOffline, false},
	}
	for _, c := range cases {
		if got := c.m.Kind(); got != c.want {
			t.Errorf("%s: kind %s, want %s", c.name, got, c.want)
		}
		if got := c.m.Talos(); got != c.talos {
			t.Errorf("%s: talos %v, want %v", c.name, got, c.talos)
		}
	}
}
