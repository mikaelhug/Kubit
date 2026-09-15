// Package cluster orchestrates the Talos layer: create, add node, remove node, upgrade.
// Every long operation reports progress through Events so the CLI and the UI's SSE
// stream see the same thing: a declared list of steps, step transitions, and log lines
// attributed to a step.
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

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
	// StepCancelled is set by the operation runner on steps interrupted by a cancel.
	StepCancelled StepStatus = "cancelled"
)

// Step is one phase of an operation, declared up front so a UI can show the whole
// path before the first log line.
type Step struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     StepStatus `json:"status"`
	Node       string     `json:"node,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Event kinds: "log" (default) is a line attributed to Step; "steps" declares the
// operation's steps; "step" reports a status transition of one step.
const (
	KindLog   = "log"
	KindSteps = "steps"
	KindStep  = "step"
)

type Event struct {
	Time    time.Time  `json:"time"`
	Kind    string     `json:"kind,omitempty"`
	Level   Level      `json:"level"`
	Step    string     `json:"step"`
	Node    string     `json:"node,omitempty"`
	Message string     `json:"message"`
	Steps   []Step     `json:"steps,omitempty"`
	Status  StepStatus `json:"status,omitempty"`
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
	s(Event{Time: time.Now(), Kind: KindLog, Level: level, Step: step, Node: node, Message: fmt.Sprintf(format, args...)})
}

// plan declares the steps of the operation in order; ids double as the Step field of
// log events, which is how lines are attributed.
func (s Sink) plan(steps ...Step) {
	if s == nil {
		return
	}
	for i := range steps {
		if steps[i].Status == "" {
			steps[i].Status = StepPending
		}
	}
	s(Event{Time: time.Now(), Kind: KindSteps, Level: Info, Steps: steps})
}

func (s Sink) begin(step string) {
	s.transition(step, StepRunning)
}

func (s Sink) end(step string) {
	s.transition(step, StepDone)
}

func (s Sink) fail(step string) {
	s.transition(step, StepFailed)
}

func (s Sink) skip(step string) {
	s.transition(step, StepSkipped)
}

func (s Sink) transition(step string, status StepStatus) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Kind: KindStep, Level: Info, Step: step, Status: status})
}

// Steps is a small helper for declaring steps inline: Steps("preflight", "Preflight", ...).
func Steps(pairs ...string) []Step {
	out := make([]Step, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Step{ID: pairs[i], Title: pairs[i+1], Status: StepPending})
	}
	return out
}

// run brackets fn with begin/end (or fail) for step.
func (s Sink) run(step string, fn func() error) error {
	s.begin(step)
	if err := fn(); err != nil {
		s.fail(step)
		return err
	}
	s.end(step)
	return nil
}

// subSink folds a nested operation's events into one step of the parent: its plan and
// step transitions are dropped, its log lines are re-attributed to step.
func subSink(parent Sink, step string) Sink {
	if parent == nil {
		return nil
	}
	return func(e Event) {
		if e.Kind != KindLog {
			return
		}
		e.Step = step
		parent(e)
	}
}
