package watch

import (
	"time"
)

const offlineAfter = 3

func isGap(lastEnd, start, end time.Time, interval time.Duration) bool {
	if lastEnd.IsZero() {
		return false
	}
	return start.Sub(lastEnd) > 2*interval || end.Sub(start) > 2*interval
}

func (w *Watcher) noteOffline(reason string) {
	w.mu.Lock()
	w.offlineTicks++
	flip := w.offlineTicks >= offlineAfter
	w.mu.Unlock()
	if flip {
		w.setOnline(false, reason)
	}
}

func (w *Watcher) noteOnline() {
	w.mu.Lock()
	w.offlineTicks = 0
	w.mu.Unlock()
	w.setOnline(true, "")
}

func (w *Watcher) resetOffline() {
	w.mu.Lock()
	w.offlineTicks = 0
	w.mu.Unlock()
}

type ObserverState struct {
	Online    bool   `json:"online"`
	Since     string `json:"since,omitempty"`
	Error     string `json:"error,omitempty"`
	Gaps24h   int    `json:"gaps24h"`
	LastGapAt string `json:"lastGapAt,omitempty"`
}

func (w *Watcher) Observer() ObserverState {
	w.mu.Lock()
	defer w.mu.Unlock()
	o := w.observer
	o.Gaps24h, o.LastGapAt = w.gapStats()
	return o
}

func (w *Watcher) gapStats() (int, string) {
	cut := time.Now().Add(-24 * time.Hour)
	kept := w.gaps[:0]
	for _, g := range w.gaps {
		if g.After(cut) {
			kept = append(kept, g)
		}
	}
	w.gaps = kept
	if len(kept) == 0 {
		return 0, ""
	}
	return len(kept), kept[len(kept)-1].UTC().Format(time.RFC3339)
}

func (w *Watcher) setOnline(online bool, reason string) {
	w.mu.Lock()
	changed := w.observer.Online != online || w.observer.Since == ""
	if changed {
		w.observer = ObserverState{Online: online, Since: time.Now().UTC().Format(time.RFC3339), Error: reason}
	}
	o := w.observer
	o.Gaps24h, o.LastGapAt = w.gapStats()
	w.mu.Unlock()
	if changed && w.OnObserver != nil {
		w.OnObserver(o)
	}
}
