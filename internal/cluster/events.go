package cluster

import (
	"fmt"
	"strings"
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
	StepRunning StepStatus = "running"
	StepFailed  StepStatus = "failed"
)

const (
	KindLog  = "log"
	KindStep = "step"
)

type Event struct {
	Time    time.Time
	Kind    string
	Level   Level
	Step    string
	Node    string
	Message string
	Status  StepStatus
}

func (e Event) String() string {
	prefix := ""
	if e.Level == Warn || e.Level == Error {
		prefix = strings.ToUpper(string(e.Level)) + " "
	}
	if e.Node != "" {
		return fmt.Sprintf("%s %s[%s] %s: %s", e.Time.Format("15:04:05"), prefix, e.Step, e.Node, e.Message)
	}
	return fmt.Sprintf("%s %s[%s] %s", e.Time.Format("15:04:05"), prefix, e.Step, e.Message)
}

type Sink func(Event)

func (s Sink) Emit(level Level, step, node, format string, args ...any) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Kind: KindLog, Level: level, Step: step, Node: node, Message: fmt.Sprintf(format, args...)})
}

func (s Sink) Run(step string, fn func() error) error {
	s.transition(step, StepRunning)
	if err := fn(); err != nil {
		s.transition(step, StepFailed)
		return err
	}
	return nil
}

func (s Sink) transition(step string, status StepStatus) {
	if s == nil {
		return
	}
	s(Event{Time: time.Now(), Kind: KindStep, Level: Info, Step: step, Status: status})
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
