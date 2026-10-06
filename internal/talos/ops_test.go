package talos

import (
	"context"
	"io"
	"testing"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc"
)

type fakeStream[T any] struct {
	grpc.ClientStream
	msgs []*T
}

func (s *fakeStream[T]) Recv() (*T, error) {
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

type fakeLifecycle struct {
	machineapi.LifecycleServiceClient
	stream *fakeStream[machineapi.LifecycleServiceUpgradeResponse]
}

func (f *fakeLifecycle) Upgrade(context.Context, *machineapi.LifecycleServiceUpgradeRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[machineapi.LifecycleServiceUpgradeResponse], error) {
	return f.stream, nil
}

func installMessage(msg string) *machineapi.LifecycleServiceUpgradeResponse {
	return &machineapi.LifecycleServiceUpgradeResponse{Progress: &machineapi.LifecycleServiceInstallProgress{Response: &machineapi.LifecycleServiceInstallProgress_Message{Message: msg}}}
}

func installExit(code int32) *machineapi.LifecycleServiceUpgradeResponse {
	return &machineapi.LifecycleServiceUpgradeResponse{Progress: &machineapi.LifecycleServiceInstallProgress{Response: &machineapi.LifecycleServiceInstallProgress_ExitCode{ExitCode: code}}}
}

func upgrade(msgs ...*machineapi.LifecycleServiceUpgradeResponse) error {
	lc := &fakeLifecycle{stream: &fakeStream[machineapi.LifecycleServiceUpgradeResponse]{msgs: msgs}}
	c := &Client{Client: &client.Client{LifecycleClient: lc}, IP: "10.0.0.5"}
	return c.LifecycleUpgrade(context.Background(), "img@sha256:feed", func(string) {})
}

func TestTalosInstallerFailureArrivesOnlyAsAnExitCodeInTheStream(t *testing.T) {
	if err := upgrade(installMessage("disk full"), installExit(1)); err == nil {
		t.Error("a non-zero exit code must fail the upgrade")
	}
	if err := upgrade(installMessage("installing")); err == nil {
		t.Error("a stream that ends without an exit code must fail the upgrade")
	}
}
