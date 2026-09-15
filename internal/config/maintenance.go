package config

import (
	"fmt"
	"strings"
	"time"
)

// Maintenance restricts when disruptive operations (upgrades, reboots, drains,
// removals, restores) may start. Empty = anytime.
type Maintenance struct {
	// Window is "<days> <HH:MM>-<HH:MM>", days a comma list of Mon..Sun or "daily";
	// the range may cross midnight, e.g. "Sat,Sun 22:00-04:00".
	Window   string `yaml:"window,omitempty" json:"window,omitempty"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"` // IANA name, default Local
}

type window struct {
	days       map[time.Weekday]bool
	start, end int // minutes since midnight; end < start crosses midnight
	loc        *time.Location
}

var weekdays = map[string]time.Weekday{"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday}

func (m Maintenance) parse() (*window, error) {
	if strings.TrimSpace(m.Window) == "" {
		return nil, nil
	}
	loc := time.Local
	if m.Timezone != "" {
		l, err := time.LoadLocation(m.Timezone)
		if err != nil {
			return nil, fmt.Errorf("maintenance.timezone %q: %w", m.Timezone, err)
		}
		loc = l
	}
	fields := strings.Fields(m.Window)
	if len(fields) != 2 {
		return nil, fmt.Errorf("maintenance.window %q: want \"<days> <HH:MM>-<HH:MM>\"", m.Window)
	}
	w := &window{days: map[time.Weekday]bool{}, loc: loc}
	if strings.EqualFold(fields[0], "daily") {
		for _, d := range weekdays {
			w.days[d] = true
		}
	} else {
		for _, d := range strings.Split(fields[0], ",") {
			wd, ok := weekdays[strings.ToLower(strings.TrimSpace(d))[:min(3, len(strings.TrimSpace(d)))]]
			if !ok {
				return nil, fmt.Errorf("maintenance.window: unknown day %q", d)
			}
			w.days[wd] = true
		}
	}
	rng := strings.SplitN(fields[1], "-", 2)
	if len(rng) != 2 {
		return nil, fmt.Errorf("maintenance.window: bad time range %q", fields[1])
	}
	var err error
	if w.start, err = hhmm(rng[0]); err != nil {
		return nil, err
	}
	if w.end, err = hhmm(rng[1]); err != nil {
		return nil, err
	}
	if w.start == w.end {
		return nil, fmt.Errorf("maintenance.window: empty range %q", fields[1])
	}
	return w, nil
}

func hhmm(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("maintenance.window: bad time %q", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Validate checks the syntax.
func (m Maintenance) Validate() error {
	_, err := m.parse()
	return err
}

// Open reports whether t falls inside the window (always true when none is set) and
// the next opening time when it does not.
func (m Maintenance) Open(t time.Time) (open bool, next time.Time) {
	w, err := m.parse()
	if err != nil || w == nil {
		return true, time.Time{}
	}
	lt := t.In(w.loc)
	if w.contains(lt) {
		return true, time.Time{}
	}
	// Scan forward at minute resolution for up to eight days.
	probe := lt.Truncate(time.Minute)
	for i := 0; i < 8*24*60; i++ {
		probe = probe.Add(time.Minute)
		if w.contains(probe) {
			return false, probe
		}
	}
	return false, time.Time{}
}

func (w *window) contains(t time.Time) bool {
	mins := t.Hour()*60 + t.Minute()
	if w.end > w.start {
		return w.days[t.Weekday()] && mins >= w.start && mins < w.end
	}
	// Crosses midnight: the part after midnight belongs to the previous day's window.
	if mins >= w.start {
		return w.days[t.Weekday()]
	}
	if mins < w.end {
		return w.days[t.AddDate(0, 0, -1).Weekday()]
	}
	return false
}
