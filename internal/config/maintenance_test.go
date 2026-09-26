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
	sat23 := time.Date(2026, 9, 19, 23, 0, 0, 0, time.UTC)
	sun03 := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	mon03 := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)
	tue03 := time.Date(2026, 9, 22, 3, 0, 0, 0, time.UTC)
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

func TestMaintenanceWindowCloses(t *testing.T) {
	m := Maintenance{Window: "Sat,Sun 22:00-04:00", Timezone: "UTC"}
	for _, tc := range []struct {
		t    time.Time
		want time.Time
	}{
		{time.Date(2026, 9, 19, 23, 0, 0, 0, time.UTC), time.Date(2026, 9, 20, 4, 0, 0, 0, time.UTC)},
		{time.Date(2026, 9, 20, 23, 30, 15, 0, time.UTC), time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC)},
		{time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC), time.Time{}},
	} {
		if got := m.Closes(tc.t); !got.Equal(tc.want) {
			t.Errorf("Closes(%s) = %s, want %s", tc.t, got, tc.want)
		}
	}
	if got := (Maintenance{}).Closes(time.Now()); !got.IsZero() {
		t.Errorf("no window never closes, got %s", got)
	}
}
