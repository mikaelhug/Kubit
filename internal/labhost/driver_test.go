package labhost

import "testing"

func TestBootCacheName(t *testing.T) {
	if got, err := BootCacheName("v1.14.1", "d9ff89777e246792e7642abd3220a616"); err != nil || got != "v1.14.1-d9ff89777e24" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := BootCacheName("v1.14.1", "short"); err == nil {
		t.Error("a short schematic must be refused")
	}
}
