package watch

import (
	"slices"
	"sync"
	"time"
)

const (
	KubitKey   = "kubit"
	eventLimit = 500
	eventAge   = 7 * 24 * time.Hour
)

type Event struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Cluster  string `json:"cluster"`
	Node     string `json:"node,omitempty"`
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Open     bool   `json:"open"`
	Notify   bool   `json:"-"`
	Recovers string `json:"-"`
}

type Alerts struct {
	OnEvent func(e Event)
	Forward func(e Event)

	mu     sync.Mutex
	next   int64
	events map[string][]Event
}

func NewAlerts() *Alerts { return &Alerts{events: map[string][]Event{}} }

func (a *Alerts) Record(e Event) {
	e.Open = false
	a.publish(a.add(e), e.Notify)
}

func (a *Alerts) Raise(e Event) bool {
	a.mu.Lock()
	if i := a.openIndex(e.Cluster, e.Node, e.Kind); i >= 0 {
		open := &a.events[e.Cluster][i]
		if open.Severity == e.Severity && open.Message == e.Message {
			a.mu.Unlock()
			return false
		}
		rose := rank[e.Severity] > rank[open.Severity]
		open.Severity, open.Message = e.Severity, e.Message
		updated := *open
		a.mu.Unlock()
		a.publish(updated, rose && e.Notify)
		return rose
	}
	e.Open = true
	e = a.addLocked(e)
	a.mu.Unlock()
	a.publish(e, e.Notify)
	return true
}

var rank = map[string]int{"info": 0, "warn": 1, "critical": 2}

func AtLeast(sev, min string) bool {
	m, ok := rank[min]
	if !ok {
		m = rank["warn"]
	}
	return rank[sev] >= m
}

func (a *Alerts) Resolve(cluster, node, kind string, recovery Event) bool {
	a.mu.Lock()
	i := a.openIndex(cluster, node, kind)
	if i < 0 {
		a.mu.Unlock()
		return false
	}
	list := a.events[cluster]
	list[i].Open = false
	closed := list[i]
	a.mu.Unlock()
	a.publish(closed, false)
	recovery.Recovers, recovery.Notify = kind, closed.Notify
	a.Record(recovery)
	return true
}

func (a *Alerts) IsOpen(cluster, node, kind string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.openIndex(cluster, node, kind) >= 0
}

func (a *Alerts) Open(cluster string) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Event
	for _, e := range a.events[cluster] {
		if e.Open {
			out = append(out, e)
		}
	}
	return out
}

func (a *Alerts) OpenCount(cluster string) int { return len(a.Open(cluster)) }

func (a *Alerts) List(cluster string, limit int) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.events[cluster]
	out := make([]Event, 0, min(limit, len(list)))
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, list[i])
	}
	return out
}

func (a *Alerts) openIndex(cluster, node, kind string) int {
	return slices.IndexFunc(a.events[cluster], func(e Event) bool { return e.Open && e.Node == node && e.Kind == kind })
}

func (a *Alerts) add(e Event) Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addLocked(e)
}

func (a *Alerts) addLocked(e Event) Event {
	a.next++
	now := time.Now().UTC()
	e.ID, e.TS = a.next, now.Format(time.RFC3339)
	list := append(a.events[e.Cluster], e)
	excess, cutoff := len(list)-eventLimit, now.Add(-eventAge)
	kept := make([]Event, 0, len(list))
	for _, old := range list {
		if !old.Open && (excess > 0 || stamped(old.TS).Before(cutoff)) {
			excess--
			continue
		}
		kept = append(kept, old)
	}
	a.events[e.Cluster] = kept
	return e
}

func stamped(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339, ts)
	return t
}

func (a *Alerts) publish(e Event, forward bool) {
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
	if forward && a.Forward != nil {
		a.Forward(e)
	}
}
