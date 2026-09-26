package pxe

import (
	"fmt"
	"testing"
	"time"
)

func TestTrackerBoundsLogAndBoots(t *testing.T) {
	tr := newTracker()
	for i := range 700 {
		tr.plain(fmt.Sprintf("aa:bb:cc:00:%02x:%02x", i/256, i%256), "")
	}
	if len(tr.log) > 500 {
		t.Errorf("plain log grew to %d lines", len(tr.log))
	}
	if len(tr.boots) > maxBoots {
		t.Errorf("boots grew to %d", len(tr.boots))
	}
	if _, ok := tr.boots["aa:bb:cc:00:02:bb"]; !ok {
		t.Error("the newest machine must be kept")
	}
}

func TestTrackerDropsDayOldBoots(t *testing.T) {
	tr := newTracker()
	tr.dhcp("aa:bb:cc:00:00:01", "amd64")
	tr.http("10.0.0.5", "amd64", "ipxe")
	tr.boots["aa:bb:cc:00:00:01"].LastSeen = time.Now().Add(-25 * time.Hour)
	tr.prune(time.Now())
	if len(tr.boots) != 0 || len(tr.byIP) != 0 {
		t.Errorf("stale boot kept: %v %v", tr.boots, tr.byIP)
	}
}
