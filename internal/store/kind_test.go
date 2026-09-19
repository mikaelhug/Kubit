package store

import "testing"

func TestMachineKind(t *testing.T) {
	cases := []struct {
		name  string
		m     Machine
		want  Kind
		talos bool
	}{
		{"armed member", Machine{Cluster: "lab", State: "ready", Provision: true, ProvisionKind: "talos"}, KindMember, true},
		{"installing member", Machine{Cluster: "lab", State: "installing"}, KindMember, true},
		{"lab host installing", Machine{State: "labhost", LabHost: &LabHost{State: "installing"}, Provision: true, ProvisionKind: "labhost"}, KindLabHost, false},
		{"lab host setup", Machine{State: "labhost", LabHost: &LabHost{State: "setup"}, Provision: true, ProvisionKind: "labhost"}, KindLabHost, false},
		{"lab host ready", Machine{State: "labhost", LabHost: &LabHost{State: "ready"}}, KindLabHost, false},
		{"lab host error", Machine{State: "labhost", LabHost: &LabHost{State: "error"}}, KindLabHost, false},
		{"maintenance", Machine{State: "maintenance"}, KindMaintenance, true},
		{"maintenance lab vm", Machine{State: "maintenance", Host: "aa:bb:cc:dd:ee:ff"}, KindMaintenance, true},
		{"configured", Machine{State: "configured"}, KindConfigured, false},
		{"armed talos", Machine{State: "amt", Provision: true, ProvisionKind: "talos"}, KindBooting, false},
		{"booting vm", Machine{State: "booting", Host: "aa:bb:cc:dd:ee:ff"}, KindBooting, false},
		{"off vm", Machine{State: "off", Host: "aa:bb:cc:dd:ee:ff"}, KindUnbooted, false},
		{"amt", Machine{State: "amt"}, KindUnbooted, false},
		{"unknown", Machine{State: "unknown"}, KindUnbooted, false},
		{"empty", Machine{}, KindUnbooted, false},
	}
	for _, c := range cases {
		if got := c.m.Kind(); got != c.want {
			t.Errorf("%s: kind %s, want %s", c.name, got, c.want)
		}
		if got := c.m.Talos(); got != c.talos {
			t.Errorf("%s: talos %v, want %v", c.name, got, c.talos)
		}
	}
	if !(&Machine{Host: "x"}).IsLabVM() || (&Machine{}).IsLabVM() {
		t.Error("IsLabVM follows Host")
	}
}
