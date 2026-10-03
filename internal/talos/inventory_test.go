package talos

import (
	"encoding/json"
	"testing"
)

func TestInventoryCarriesTPMAndWatchdog(t *testing.T) {
	b, err := json.Marshal(Inventory{TPM: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["tpm"] != true || m["watchdog"] != false {
		t.Errorf("inventory JSON: %s", b)
	}
	var old Inventory
	if err := json.Unmarshal([]byte(`{"kvm":true}`), &old); err != nil || old.TPM || old.Watchdog {
		t.Errorf("a record from before detection reads as absent: %+v %v", old, err)
	}
}
