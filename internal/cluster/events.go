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
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepDone      StepStatus = "done"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCancelled StepStatus = "cancelled"
)

type Step struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     StepStatus `json:"status"`
	Node       string     `json:"node,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

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

type Sink func(Event)

func (s Sink) Emit(level Level, step, node, format string, args ...any) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Kind: KindLog, Level: level, Step: step, Node: node, Message: fmt.Sprintf(format, args...)})
}

func (s Sink) Plan(steps ...Step) {
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

func (s Sink) Begin(step string) {
	s.transition(step, StepRunning)
}

func (s Sink) End(step string) {
	s.transition(step, StepDone)
}

func (s Sink) Fail(step string) {
	s.transition(step, StepFailed)
}

func (s Sink) Skip(step string) {
	s.transition(step, StepSkipped)
}

func (s Sink) transition(step string, status StepStatus) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Kind: KindStep, Level: Info, Step: step, Status: status})
}

func Steps(pairs ...string) []Step {
	out := make([]Step, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Step{ID: pairs[i], Title: pairs[i+1], Status: StepPending})
	}
	return out
}

func (s Sink) Run(step string, fn func() error) error {
	s.Begin(step)
	if err := fn(); err != nil {
		s.Fail(step)
		return err
	}
	s.End(step)
	return nil
}

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
