package talos

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

func TestWatchStageReportsChangesUntilCancelled(t *testing.T) {
	st := state.WrapCore(namespaced.NewState(inmem.Build))
	ctx, cancel := context.WithCancel(t.Context())
	ms := runtime.NewMachineStatus()
	ms.TypedSpec().Stage = runtime.MachineStageBooting
	if err := st.Create(ctx, ms); err != nil {
		t.Fatal(err)
	}
	stages := make(chan string, 8)
	done := make(chan error, 1)
	go func() { done <- watchStage(ctx, st, func(s string) { stages <- s }) }()
	next := func() string {
		select {
		case s := <-stages:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no stage reported")
			return ""
		}
	}
	if s := next(); s != runtime.MachineStageBooting.String() {
		t.Fatalf("initial stage %q", s)
	}
	set := func(stage runtime.MachineStage) {
		cur, err := st.Get(ctx, ms.Metadata())
		if err != nil {
			t.Fatal(err)
		}
		up := cur.DeepCopy().(*runtime.MachineStatus)
		up.TypedSpec().Stage = stage
		if err := st.Update(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	set(runtime.MachineStageBooting)
	set(runtime.MachineStageRunning)
	if s := next(); s != runtime.MachineStageRunning.String() {
		t.Fatalf("an unchanged stage was reported or the change was missed: %q", s)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch outlived its context")
	}
}

func TestWatchStageWithoutMachineStatusWaits(t *testing.T) {
	st := state.WrapCore(namespaced.NewState(inmem.Build))
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	reported := false
	if err := watchStage(ctx, st, func(string) { reported = true }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ended with %v", err)
	}
	if reported {
		t.Error("a missing resource reported a stage")
	}
}

type erroring struct {
	state.State
	err error
}

func (e erroring) Watch(ctx context.Context, _ resource.Pointer, ch chan<- state.Event, _ ...state.WatchOption) error {
	go func() {
		select {
		case ch <- state.Event{Type: state.Errored, Error: e.err}:
		case <-ctx.Done():
		}
	}()
	return nil
}

func TestWatchStageEndsOnErroredEvent(t *testing.T) {
	broken := errors.New("stream reset")
	st := erroring{State: state.WrapCore(namespaced.NewState(inmem.Build)), err: broken}
	done := make(chan error, 1)
	go func() { done <- watchStage(t.Context(), st, func(string) {}) }()
	select {
	case err := <-done:
		if !errors.Is(err, broken) {
			t.Errorf("ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an errored watch kept running")
	}
}
