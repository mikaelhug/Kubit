package talos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (c *Client) Apply(ctx context.Context, cfg []byte) error {
	_, err := c.ApplyConfiguration(c.nodeContext(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg,
		Mode: machineapi.ApplyConfigurationRequest_AUTO,
	})
	return err
}

func (c *Client) ApplyTry(ctx context.Context, cfg []byte, rollback time.Duration) error {
	_, err := c.ApplyConfiguration(c.nodeContext(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_TRY, TryModeTimeout: durationpb.New(rollback),
	})
	return err
}

func (c *Client) ApplyNoReboot(ctx context.Context, cfg []byte) error {
	_, err := c.ApplyConfiguration(c.nodeContext(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_NO_REBOOT,
	})
	return err
}

func (c *Client) Hostname(ctx context.Context) (string, error) {
	hn, err := safe.StateGetByID[*network.HostnameStatus](c.nodeContext(ctx), c.COSI, network.HostnameID)
	if err != nil {
		return "", err
	}
	return hn.TypedSpec().Hostname, nil
}

func (c *Client) ApplyDryRun(ctx context.Context, cfg []byte) (string, error) {
	resp, err := c.ApplyConfiguration(c.nodeContext(ctx), &machineapi.ApplyConfigurationRequest{
		Data: cfg, Mode: machineapi.ApplyConfigurationRequest_AUTO, DryRun: true,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Messages) == 0 {
		return "", nil
	}
	return resp.Messages[0].ModeDetails, nil
}

func (c *Client) MachineConfig(ctx context.Context) ([]byte, error) {
	mc, err := safe.StateGetByID[*configres.MachineConfig](c.nodeContext(ctx), c.COSI, configres.ActiveID)
	if err != nil {
		return nil, err
	}
	return mc.Provider().Bytes()
}

func (c *Client) VersionTag(ctx context.Context) (string, error) {
	v, err := c.Version(c.nodeContext(ctx))
	if err != nil {
		return "", err
	}
	if len(v.Messages) == 0 || v.Messages[0].Version == nil {
		return "", errors.New("empty Version response")
	}
	return v.Messages[0].Version.Tag, nil
}

func (c *Client) InstalledSchematic(ctx context.Context) (string, error) {
	if s, err := safe.StateGetByID[*runtime.ImageFactorySchematic](c.nodeContext(ctx), c.COSI, runtime.ImageFactorySchematicID); err == nil && s.TypedSpec().SchematicID != "" {
		return s.TypedSpec().SchematicID, nil
	}
	exts, err := safe.StateListAll[*runtime.ExtensionStatus](c.nodeContext(ctx), c.COSI)
	if err != nil {
		return "", err
	}
	for e := range exts.All() {
		if m := e.TypedSpec().Metadata; m.Name == constants.ImageFactorySchematicExtensionName {
			return m.Version, nil
		}
	}
	return "", nil
}

func (c *Client) RebootMachine(ctx context.Context) error {
	return c.Reboot(c.nodeContext(ctx))
}

func systemContainerd() *common.ContainerdInstance {
	return &common.ContainerdInstance{Driver: common.ContainerDriver_CRI, Namespace: common.ContainerdNamespace_NS_SYSTEM}
}

func (c *Client) PullImage(ctx context.Context, ref string, progress func(string)) (string, error) {
	stream, err := c.ImageClient.Pull(c.nodeContext(ctx), &machineapi.ImageServicePullRequest{Containerd: systemContainerd(), ImageRef: ref})
	if err != nil {
		return "", err
	}
	pull := pullProgress{report: progress, every: pullReportEvery, layers: map[string]layerProgress{}}
	name := ref
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			pull.flush()
			return name, nil
		}
		if err != nil {
			return "", err
		}
		switch r := resp.GetResponse().(type) {
		case *machineapi.ImageServicePullResponse_Name:
			name = r.Name
		case *machineapi.ImageServicePullResponse_PullProgress:
			pull.update(r.PullProgress)
		}
	}
}

func (c *Client) LifecycleUpgrade(ctx context.Context, image string, progress func(string)) error {
	stream, err := c.LifecycleClient.Upgrade(c.nodeContext(ctx), &machineapi.LifecycleServiceUpgradeRequest{
		Containerd: systemContainerd(),
		Source:     &machineapi.InstallArtifactsSource{ImageName: image},
	})
	if err != nil {
		return err
	}
	var (
		tail   []string
		exit   int32
		exited bool
	)
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch r := resp.GetProgress().GetResponse().(type) {
		case *machineapi.LifecycleServiceInstallProgress_Message:
			for _, line := range strings.Split(r.Message, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					progress(line)
					tail = append(tail[max(0, len(tail)-installTail+1):], line)
				}
			}
		case *machineapi.LifecycleServiceInstallProgress_ExitCode:
			exit, exited = r.ExitCode, true
		}
	}
	switch {
	case !exited:
		return errors.New("installer ended without an exit code")
	case exit != 0 && len(tail) > 0:
		return fmt.Errorf("installer exited with code %d: %s", exit, strings.Join(tail, "; "))
	case exit != 0:
		return fmt.Errorf("installer exited with code %d", exit)
	}
	return nil
}

const (
	pullReportEvery = 3 * time.Second
	installTail     = 5
)

type layerProgress struct {
	done, total int64
	complete    bool
}

type pullProgress struct {
	report func(string)
	every  time.Duration
	last   time.Time
	dirty  bool
	layers map[string]layerProgress
}

func (p *pullProgress) update(m *machineapi.ImageServicePullProgress) {
	l := p.layers[m.GetLayerId()]
	lp := m.GetProgress()
	if lp.GetTotal() > 0 {
		l.total = lp.GetTotal()
	}
	switch lp.GetStatus() {
	case machineapi.ImageServicePullLayerProgress_DOWNLOADING:
		l.done = lp.GetOffset()
	case machineapi.ImageServicePullLayerProgress_EXTRACT_COMPLETE, machineapi.ImageServicePullLayerProgress_ALREADY_EXISTS:
		l.done, l.complete = l.total, true
	default:
		l.done = l.total
	}
	p.layers[m.GetLayerId()] = l
	p.dirty = true
	if now := time.Now(); now.Sub(p.last) >= p.every {
		p.last = now
		p.flush()
	}
}

func (p *pullProgress) flush() {
	if !p.dirty {
		return
	}
	p.dirty = false
	var done, total int64
	complete := 0
	for _, l := range p.layers {
		done += l.done
		total += l.total
		if l.complete {
			complete++
		}
	}
	p.report(fmt.Sprintf("pulling: %d of %d MiB, %d of %d layers done", done>>20, total>>20, complete, len(p.layers)))
}

func (c *Client) ResetToMaintenance(ctx context.Context) error {
	return c.Reset(c.nodeContext(ctx), true, true)
}

func (c *Client) WipeToMaintenance(ctx context.Context) error {
	return c.ResetGeneric(c.nodeContext(ctx), &machineapi.ResetRequest{
		Graceful: false,
		Reboot:   true,
		SystemPartitionsToWipe: []*machineapi.ResetPartitionSpec{
			{Label: "STATE", Wipe: true},
			{Label: "EPHEMERAL", Wipe: true},
		},
	})
}

func (c *Client) AdminKubeconfig(ctx context.Context) ([]byte, error) {
	return c.Kubeconfig(c.nodeContext(ctx))
}

func (c *Client) BootstrapEtcd(ctx context.Context) error {
	return c.Bootstrap(c.nodeContext(ctx), &machineapi.BootstrapRequest{})
}

func (c *Client) Services(ctx context.Context) ([]*machineapi.ServiceInfo, error) {
	resp, err := c.ServiceList(c.nodeContext(ctx))
	if err != nil {
		return nil, err
	}
	var out []*machineapi.ServiceInfo
	for _, m := range resp.Messages {
		out = append(out, m.Services...)
	}
	return out, nil
}

func (c *Client) KernelLog(ctx context.Context, follow bool) (machineapi.MachineService_DmesgClient, error) {
	return c.Dmesg(c.nodeContext(ctx), follow, false)
}

func (c *Client) ServiceLog(ctx context.Context, id string, follow bool, tail int32) (machineapi.MachineService_LogsClient, error) {
	return c.Logs(c.nodeContext(ctx), "system", 0, id, follow, tail)
}

func (c *Client) ServiceHealthy(ctx context.Context, id string) (bool, error) {
	infos, err := c.ServiceInfo(c.nodeContext(ctx), id)
	if err != nil {
		return false, err
	}
	for _, i := range infos {
		if i.Service == nil {
			continue
		}
		if i.Service.State == "Running" && i.Service.Health != nil && i.Service.Health.Healthy {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) EtcdMemberCount(ctx context.Context) (int, error) {
	resp, err := c.EtcdMemberList(c.nodeContext(ctx), &machineapi.EtcdMemberListRequest{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range resp.Messages {
		n += len(m.Members)
	}
	return n, nil
}

type EtcdPeer struct {
	ID       uint64
	Hostname string
	PeerURLs []string
}

func (c *Client) EtcdPeers(ctx context.Context) ([]EtcdPeer, error) {
	resp, err := c.EtcdMemberList(c.nodeContext(ctx), &machineapi.EtcdMemberListRequest{})
	if err != nil {
		return nil, err
	}
	var out []EtcdPeer
	for _, m := range resp.Messages {
		for _, mm := range m.Members {
			out = append(out, EtcdPeer{ID: mm.Id, Hostname: mm.Hostname, PeerURLs: mm.PeerUrls})
		}
	}
	return out, nil
}

func (c *Client) EtcdAlarms(ctx context.Context) ([]string, error) {
	resp, err := c.EtcdAlarmList(c.nodeContext(ctx))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, msg := range resp.Messages {
		for _, a := range msg.MemberAlarms {
			out = append(out, a.Alarm.String())
		}
	}
	return out, nil
}

func (c *Client) BootID(ctx context.Context) (string, error) {
	resp, err := c.MachineClient.SystemStat(c.nodeContext(ctx), &emptypb.Empty{})
	if err != nil {
		return "", err
	}
	if len(resp.Messages) == 0 {
		return "", errors.New("empty SystemStat response")
	}
	return strconv.FormatUint(resp.Messages[0].BootTime, 10), nil
}

func WaitForReboot(ctx context.Context, ip string, talosconfig []byte, prevBootID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	sawCreds := false
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if PortOpen(ctx, ip, 2*time.Second) {
			attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
			id, err := bootIDWith(attempt, ip, talosconfig)
			cancel()
			switch {
			case err == nil && id != prevBootID:
				return nil
			case err == nil:
				sawCreds = true
				last = NotReady("still on the pre-install boot")
			default:
				last = err
				if sawCreds {
					if r := Probe(ctx, ip, 2*time.Second); r.Err == nil && r.State == StateMaintenance {
						return fmt.Errorf("%s rebooted into maintenance mode, not the installed system — the disk-boot switch did not take", ip)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("%s: not back after reboot within %s: %w", ip, timeout, last)
}

func bootIDWith(ctx context.Context, ip string, talosconfig []byte) (string, error) {
	c, err := Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.BootID(ctx)
}

func Stage(ctx context.Context, ip string, talosconfig []byte) (string, error) {
	c, err := Dial(ctx, ip, talosconfig)
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.Stage(ctx)
}

func (c *Client) Stage(ctx context.Context) (string, error) {
	st, err := safe.StateGetByID[*runtime.MachineStatus](c.nodeContext(ctx), c.COSI, runtime.MachineStatusID)
	if err != nil {
		return "", err
	}
	return st.TypedSpec().Stage.String(), nil
}

func Retry(ctx context.Context, timeout, interval time.Duration, f func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := f()
		if err == nil {
			return nil
		}
		if !retryable(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

type NotReady string

func (e NotReady) Error() string { return string(e) }

func retryable(err error) bool {
	var nr NotReady
	if errors.As(err, &nr) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if _, isGRPC := status.FromError(err); !isGRPC {
		return false
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.FailedPrecondition, codes.Aborted, codes.Unknown:
		return true
	}
	return false
}

func (c *Client) BootstrapManifests(ctx context.Context) ([]map[string]any, error) {
	list, err := safe.StateListAll[*k8s.Manifest](c.nodeContext(ctx), c.COSI)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for m := range list.All() {
		for _, item := range m.TypedSpec().Items {
			out = append(out, item.Object)
		}
	}
	return out, nil
}

func (c *Client) VarAvailable(ctx context.Context) (avail, size uint64, err error) {
	resp, err := c.Mounts(c.nodeContext(ctx))
	if err != nil {
		return 0, 0, err
	}
	for _, msg := range resp.Messages {
		for _, st := range msg.Stats {
			if st.MountedOn == "/var" {
				return st.Available, st.Size, nil
			}
		}
	}
	return 0, 0, errors.New("/var not found in mounts")
}
