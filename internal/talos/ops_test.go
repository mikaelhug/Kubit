package talos

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/api/common"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/grpc"
)

type fakeStream[T any] struct {
	grpc.ClientStream
	msgs []*T
	err  error
}

func (s *fakeStream[T]) Recv() (*T, error) {
	if len(s.msgs) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

type fakeImages struct {
	machineapi.ImageServiceClient
	req    *machineapi.ImageServicePullRequest
	stream *fakeStream[machineapi.ImageServicePullResponse]
}

func (f *fakeImages) Pull(_ context.Context, in *machineapi.ImageServicePullRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[machineapi.ImageServicePullResponse], error) {
	f.req = in
	return f.stream, nil
}

type fakeLifecycle struct {
	machineapi.LifecycleServiceClient
	req    *machineapi.LifecycleServiceUpgradeRequest
	stream *fakeStream[machineapi.LifecycleServiceUpgradeResponse]
}

func (f *fakeLifecycle) Upgrade(_ context.Context, in *machineapi.LifecycleServiceUpgradeRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[machineapi.LifecycleServiceUpgradeResponse], error) {
	f.req = in
	return f.stream, nil
}

func layer(id string, st machineapi.ImageServicePullLayerProgress_Status, offset, total int64) *machineapi.ImageServicePullResponse {
	return &machineapi.ImageServicePullResponse{Response: &machineapi.ImageServicePullResponse_PullProgress{PullProgress: &machineapi.ImageServicePullProgress{
		LayerId:  id,
		Progress: &machineapi.ImageServicePullLayerProgress{Status: st, Offset: offset, Total: total},
	}}}
}

func installMessage(msg string) *machineapi.LifecycleServiceUpgradeResponse {
	return &machineapi.LifecycleServiceUpgradeResponse{Progress: &machineapi.LifecycleServiceInstallProgress{Response: &machineapi.LifecycleServiceInstallProgress_Message{Message: msg}}}
}

func installExit(code int32) *machineapi.LifecycleServiceUpgradeResponse {
	return &machineapi.LifecycleServiceUpgradeResponse{Progress: &machineapi.LifecycleServiceInstallProgress{Response: &machineapi.LifecycleServiceInstallProgress_ExitCode{ExitCode: code}}}
}

func fakeClient(images machineapi.ImageServiceClient, lifecycle machineapi.LifecycleServiceClient) *Client {
	return &Client{Client: &client.Client{ImageClient: images, LifecycleClient: lifecycle}, IP: "10.0.0.5"}
}

func TestPullImageStreamsProgress(t *testing.T) {
	const ref = "factory.talos.dev/installer/abc:v1.14.2"
	msgs := []*machineapi.ImageServicePullResponse{}
	for off := int64(0); off <= 100<<20; off += 1 << 20 {
		msgs = append(msgs, layer("a", machineapi.ImageServicePullLayerProgress_DOWNLOADING, off, 100<<20))
	}
	msgs = append(msgs,
		layer("b", machineapi.ImageServicePullLayerProgress_ALREADY_EXISTS, 0, 20<<20),
		layer("a", machineapi.ImageServicePullLayerProgress_DOWNLOAD_COMPLETE, 0, 0),
		layer("a", machineapi.ImageServicePullLayerProgress_EXTRACTING, 0, 0),
		layer("a", machineapi.ImageServicePullLayerProgress_EXTRACT_COMPLETE, 0, 0),
		&machineapi.ImageServicePullResponse{Response: &machineapi.ImageServicePullResponse_Name{Name: "factory.talos.dev/installer/abc@sha256:feed"}},
	)
	images := &fakeImages{stream: &fakeStream[machineapi.ImageServicePullResponse]{msgs: msgs}}
	var lines []string
	name, err := fakeClient(images, nil).PullImage(context.Background(), ref, func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatal(err)
	}
	if name != "factory.talos.dev/installer/abc@sha256:feed" {
		t.Errorf("name: %q", name)
	}
	if images.req.GetImageRef() != ref || images.req.GetContainerd().GetNamespace() != common.ContainerdNamespace_NS_SYSTEM || images.req.GetContainerd().GetDriver() != common.ContainerDriver_CRI {
		t.Errorf("request: %v", images.req)
	}
	if len(lines) != 2 {
		t.Fatalf("progress must be coalesced to a first and a final line: %q", lines)
	}
	if lines[1] != "pulling: 120 of 120 MiB, 2 of 2 layers done" {
		t.Errorf("final line: %q", lines[1])
	}
}

func TestPullImageWithoutANameKeepsTheReference(t *testing.T) {
	images := &fakeImages{stream: &fakeStream[machineapi.ImageServicePullResponse]{}}
	var lines []string
	name, err := fakeClient(images, nil).PullImage(context.Background(), "ref:v1", func(s string) { lines = append(lines, s) })
	if err != nil || name != "ref:v1" || len(lines) != 0 {
		t.Errorf("already pulled: %q %v %q", name, err, lines)
	}
}

func TestLifecycleUpgradeNonZeroExit(t *testing.T) {
	msgs := []*machineapi.LifecycleServiceUpgradeResponse{}
	for _, m := range []string{"one", "two", "three", "four\nfive", "six", "  ", "disk full"} {
		msgs = append(msgs, installMessage(m))
	}
	msgs = append(msgs, installExit(1))
	lc := &fakeLifecycle{stream: &fakeStream[machineapi.LifecycleServiceUpgradeResponse]{msgs: msgs}}
	var lines []string
	err := fakeClient(nil, lc).LifecycleUpgrade(context.Background(), "img@sha256:feed", func(s string) { lines = append(lines, s) })
	if err == nil {
		t.Fatal("a non-zero exit code must fail")
	}
	if want := "installer exited with code 1: three; four; five; six; disk full"; err.Error() != want {
		t.Errorf("error: %q, want %q", err, want)
	}
	if strings.Join(lines, "|") != "one|two|three|four|five|six|disk full" {
		t.Errorf("progress: %q", lines)
	}
}

func TestLifecycleUpgradeSucceeds(t *testing.T) {
	lc := &fakeLifecycle{stream: &fakeStream[machineapi.LifecycleServiceUpgradeResponse]{msgs: []*machineapi.LifecycleServiceUpgradeResponse{
		installMessage("installing"), installExit(0),
	}}}
	var lines []string
	if err := fakeClient(nil, lc).LifecycleUpgrade(context.Background(), "img@sha256:feed", func(s string) { lines = append(lines, s) }); err != nil {
		t.Fatal(err)
	}
	if lc.req.GetSource().GetImageName() != "img@sha256:feed" || lc.req.GetContainerd().GetNamespace() != common.ContainerdNamespace_NS_SYSTEM {
		t.Errorf("request: %v", lc.req)
	}
	if len(lines) != 1 || lines[0] != "installing" {
		t.Errorf("progress: %q", lines)
	}
	cut := &fakeLifecycle{stream: &fakeStream[machineapi.LifecycleServiceUpgradeResponse]{msgs: []*machineapi.LifecycleServiceUpgradeResponse{installMessage("installing")}}}
	if err := fakeClient(nil, cut).LifecycleUpgrade(context.Background(), "img", func(string) {}); err == nil {
		t.Error("a stream without an exit code must fail")
	}
	broken := &fakeLifecycle{stream: &fakeStream[machineapi.LifecycleServiceUpgradeResponse]{err: io.ErrUnexpectedEOF}}
	if err := fakeClient(nil, broken).LifecycleUpgrade(context.Background(), "img", func(string) {}); err != io.ErrUnexpectedEOF {
		t.Errorf("stream error: %v", err)
	}
}
