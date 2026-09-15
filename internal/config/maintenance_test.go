package config

import (
	"testing"
	"time"
)

func TestMaintenanceWindow(t *testing.T) {
	m := Maintenance{Window: "Sat,Sun 22:00-04:00", Timezone: "UTC"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	sat23 := time.Date(2026, 9, 19, 23, 0, 0, 0, time.UTC) // Saturday
	sun03 := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)  // Sunday early: belongs to Saturday's window
	mon03 := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)  // Monday early: Sunday's window
	tue03 := time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)  // Tuesday early: Monday not in days
	wed12 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{{sat23, true}, {sun03, true}, {mon03, true}, {tue03, false}, {wed12, false}} {
		if got, _ := m.Open(tc.t); got != tc.want {
			t.Errorf("Open(%s) = %v, want %v", tc.t, got, tc.want)
		}
	}
	open, next := m.Open(wed12)
	if open || !next.Equal(time.Date(2026, 9, 19, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("next opening from Wed = %s, want Sat 22:00", next)
	}
	if err := (Maintenance{Window: "Sat 25:00-26:00"}).Validate(); err == nil {
		t.Error("bad time accepted")
	}
	if open, _ := (Maintenance{}).Open(wed12); !open {
		t.Error("empty window must be open")
	}
	if open, _ := (Maintenance{Window: "daily 01:00-02:00", Timezone: "UTC"}).Open(time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)); !open {
		t.Error("daily window closed")
	}
}
