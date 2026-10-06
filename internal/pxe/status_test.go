package pxe

import (
	"fmt"
	"testing"
)

func TestPXETrackerStaysBoundedOnABusyLAN(t *testing.T) {
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
