// Package cluster orchestrates the Talos layer: create, add node, remove node, upgrade.
// Every long operation reports progress through Events so the CLI and the UI's SSE
// stream see the same thing.
package cluster

import (
	"fmt"
	"time"
)

type Level string

const (
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
	Done  Level = "done"
)

type Event struct {
	Time    time.Time `json:"time"`
	Level   Level     `json:"level"`
	Step    string    `json:"step"`
	Node    string    `json:"node,omitempty"`
	Message string    `json:"message"`
}

func (e Event) String() string {
	if e.Node != "" {
		return fmt.Sprintf("%s [%s] %s: %s", e.Time.Format("15:04:05"), e.Step, e.Node, e.Message)
	}
	return fmt.Sprintf("%s [%s] %s", e.Time.Format("15:04:05"), e.Step, e.Message)
}

// Sink receives events; nil sinks are allowed.
type Sink func(Event)

func (s Sink) emit(level Level, step, node, format string, args ...any) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Level: level, Step: step, Node: node, Message: fmt.Sprintf(format, args...)})
}
