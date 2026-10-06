package watch

import (
	"sync"
	"time"
)

const (
	sampleSpan = 24 * time.Hour
	fineSpan   = 30 * time.Minute
)

type Sample struct {
	TS        string `json:"ts"`
	Node      string `json:"node,omitempty"`
	CPUMilli  int64  `json:"cpuMilli"`
	CPUCap    int64  `json:"cpuCap"`
	MemBytes  int64  `json:"memBytes"`
	MemCap    int64  `json:"memCap"`
	Pods      int    `json:"pods"`
	Ready     bool   `json:"ready"`
	Reachable bool   `json:"reachable"`
}

type sampleRing struct {
	mu     sync.Mutex
	series map[string][]Sample
	fine   map[string][]Sample
}

func newSampleRing() *sampleRing {
	return &sampleRing{series: map[string][]Sample{}, fine: map[string][]Sample{}}
}

func seriesKey(cluster, node string) string { return cluster + "|" + node }

func (r *sampleRing) add(cluster string, at time.Time, samples []Sample) {
	at = at.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range samples {
		k := seriesKey(cluster, s.Node)
		r.series[k] = record(r.series[k], s, at.Truncate(time.Minute), at.Add(-sampleSpan))
		r.fine[k] = record(r.fine[k], s, at.Truncate(time.Second), at.Add(-fineSpan))
	}
}

func record(list []Sample, s Sample, stamp, oldest time.Time) []Sample {
	s.TS = stamp.Format(time.RFC3339)
	if n := len(list); n > 0 && list[n-1].TS == s.TS {
		list[n-1] = s
	} else {
		list = append(list, s)
	}
	from := oldest.Format(time.RFC3339)
	cut := 0
	for cut < len(list) && list[cut].TS < from {
		cut++
	}
	return list[cut:]
}

func (r *sampleRing) window(cluster, node string, span time.Duration, now time.Time) []Sample {
	from := now.UTC().Add(-span).Format(time.RFC3339)
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.series
	if span <= fineSpan {
		src = r.fine
	}
	out := []Sample{}
	for _, s := range src[seriesKey(cluster, node)] {
		if s.TS >= from {
			out = append(out, s)
		}
	}
	return out
}
