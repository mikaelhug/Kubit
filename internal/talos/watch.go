package talos

import (
	"context"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

func WatchStage(ctx context.Context, ip string, talosconfig []byte, report func(stage string)) error {
	c, err := Dial(ctx, ip, talosconfig)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.WatchStage(ctx, report)
}

func (c *Client) WatchStage(ctx context.Context, report func(stage string)) error {
	return watchStage(c.nodeContext(ctx), c.COSI, report)
}

func watchStage(ctx context.Context, st state.CoreStateWatcher, report func(stage string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan safe.WrappedStateEvent[*runtime.MachineStatus])
	if err := safe.StateWatch(ctx, st, runtime.NewMachineStatus().Metadata(), events); err != nil {
		return err
	}
	last := ""
	for {
		var ev safe.WrappedStateEvent[*runtime.MachineStatus]
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev = <-events:
		}
		switch ev.Type() {
		case state.Errored:
			return ev.Error()
		case state.Created, state.Updated:
			ms, err := ev.Resource()
			if err != nil {
				continue
			}
			if stage := ms.TypedSpec().Stage.String(); stage != last {
				last = stage
				report(stage)
			}
		}
	}
}
