package oob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman"
	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/boot"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
	"github.com/mikael/kubit/internal/netx"
)

type Config struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	User     string `json:"user"`
	Password string `json:"password"`
	TLS      bool   `json:"tls"`
}

type Info struct {
	Version      string     `json:"version"`
	MAC          string     `json:"mac"`
	UUID         string     `json:"uuid,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	Model        string     `json:"model,omitempty"`
	Serial       string     `json:"serial,omitempty"`
	Power        string     `json:"power"`
	CPUs         int        `json:"cpus,omitempty"`
	MemoryBytes  int64      `json:"memoryBytes,omitempty"`
	Disks        []DiskInfo `json:"disks,omitempty"`
}

type DiskInfo struct {
	Model     string `json:"model,omitempty"`
	Serial    string `json:"serial,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	Transport string `json:"transport,omitempty"`
	Media     string `json:"media,omitempty"`
}

type Action string

const (
	PowerOn    Action = "on"
	PowerOff   Action = "off"
	Reset      Action = "reset"
	PowerCycle Action = "cycle"
	BootPXE    Action = "pxe"
)

type Manager interface {
	Probe(ctx context.Context) (Info, error)
	Power(ctx context.Context, a Action) error
}

type Option func(*options)

type options struct{ trace func(string) }

func WithTrace(fn func(string)) Option { return func(o *options) { o.trace = fn } }

func Open(c Config, opts ...Option) (Manager, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	switch c.Type {
	case "amt":
		if c.Host == "" || c.User == "" || c.Password == "" {
			return nil, errors.New("AMT needs host, user and password")
		}
		return &amt{c: c, tracer: o.trace}, nil
	case "redfish":
		if c.Host == "" || c.User == "" || c.Password == "" {
			return nil, errors.New("Redfish needs the BMC address, user and password")
		}
		return newRedfish(c, o.trace), nil
	}
	return nil, errors.New("no out-of-band management configured")
}

func Alive(ctx context.Context, c Config) error {
	m, err := Open(c)
	if err != nil {
		return err
	}
	switch m := m.(type) {
	case *amt:
		return m.alive(ctx)
	case *redfish:
		return m.alive(ctx)
	}
	_, err = m.Probe(ctx)
	return err
}

func Label(typ string) string {
	switch typ {
	case "amt":
		return "Intel AMT"
	case "redfish":
		return "BMC (Redfish)"
	}
	return "remote management"
}

type tracer func(string)

func (t tracer) tracef(format string, args ...any) {
	if t != nil {
		t(fmt.Sprintf(format, args...))
	}
}

type amt struct {
	c Config
	tracer
	transport http.RoundTripper
}

const wsmanCallTimeout = 30 * time.Second

type wsmanSession struct {
	wsman.Messages
	ctx    context.Context
	target *client.Target
	budget time.Duration
}

func (a *amt) session(ctx context.Context) *wsmanSession {
	m := wsman.NewMessages(client.Parameters{
		Target: a.c.Host, Username: a.c.User, Password: a.c.Password,
		UseDigest: true, UseTLS: a.c.TLS, SelfSignedAllowed: true, Timeout: wsmanCallTimeout, Transport: a.transport,
	})
	s := &wsmanSession{Messages: m, ctx: ctx}
	if t, ok := m.Client.(*client.Target); ok {
		s.target, s.budget = t, t.Timeout
	}
	return s
}

func (s *wsmanSession) next() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if s.target == nil {
		return nil
	}
	s.target.Timeout = s.budget
	if d, ok := s.ctx.Deadline(); ok {
		rem := time.Until(d)
		if rem <= 0 {
			return context.DeadlineExceeded
		}
		s.target.Timeout = min(s.budget, rem)
	}
	return nil
}

func (a *amt) alive(ctx context.Context) error {
	s := a.session(ctx)
	if err := s.next(); err != nil {
		return fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	if _, err := s.CIM.SoftwareIdentity.Enumerate(); err != nil {
		return fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	return nil
}

func (a *amt) Probe(ctx context.Context) (Info, error) {
	m := a.session(ctx)
	var info Info
	if err := m.next(); err != nil {
		return info, fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	sw, err := m.CIM.SoftwareIdentity.Enumerate()
	if err != nil {
		return info, fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	if m.next() == nil {
		if p, err := m.CIM.SoftwareIdentity.Pull(sw.Body.EnumerateResponse.EnumerationContext); err == nil {
			for _, s := range p.Body.PullResponse.SoftwareIdentityItems {
				if s.InstanceID == "AMT" {
					info.Version = s.VersionString
				}
			}
		}
	}
	if m.next() == nil {
		if e, err := m.AMT.EthernetPortSettings.Enumerate(); err == nil && m.next() == nil {
			if p, err := m.AMT.EthernetPortSettings.Pull(e.Body.EnumerateResponse.EnumerationContext); err == nil {
				for _, port := range p.Body.PullResponse.EthernetPortItems {
					if mac := netx.Normalize(port.MACAddress); mac != "" && !strings.Contains(strings.ToLower(port.InstanceID), "wireless") {
						info.MAC = mac
						break
					}
				}
			}
		}
	}
	if m.next() == nil {
		if e, err := m.CIM.Chassis.Enumerate(); err == nil && m.next() == nil {
			if p, err := m.CIM.Chassis.Pull(e.Body.EnumerateResponse.EnumerationContext); err == nil && len(p.Body.PullResponse.PackageItems) > 0 {
				ch := p.Body.PullResponse.PackageItems[0]
				info.Manufacturer, info.Model, info.Serial = ch.Manufacturer, ch.Model, ch.SerialNumber
			}
		}
	}
	info.Power = a.powerState(m)
	return info, nil
}

func (a *amt) powerState(m *wsmanSession) string {
	if m.next() != nil {
		return "unknown"
	}
	e, err := m.CIM.AssociatedPowerManagementService.Enumerate()
	if err != nil || m.next() != nil {
		return "unknown"
	}
	p, err := m.CIM.AssociatedPowerManagementService.Pull(e.Body.EnumerateResponse.EnumerationContext)
	if err != nil || len(p.Body.PullResponse.AssociatedPowerManagementServiceItems) == 0 {
		return "unknown"
	}
	switch p.Body.PullResponse.AssociatedPowerManagementServiceItems[0].PowerState {
	case 2:
		return "on"
	case 3, 4:
		return "sleep"
	case 6, 8, 12, 13:
		return "off"
	case 7:
		return "hibernate"
	}
	return "unknown"
}

func (a *amt) Power(ctx context.Context, act Action) error {
	m := a.session(ctx)
	var state power.PowerState
	switch act {
	case PowerOn:
		state = power.PowerOn
	case PowerOff:
		state = power.PowerOffHard
	case Reset:
		state = power.MasterBusReset
	case PowerCycle:
		state = power.PowerCycleOffHard
	case BootPXE:
		if err := a.forcePXE(m); err != nil {
			return err
		}
		if a.powerState(m) == "on" {
			state = power.MasterBusReset
		} else {
			state = power.PowerOn
		}
	default:
		return fmt.Errorf("unknown power action %q", act)
	}
	if err := m.next(); err != nil {
		return fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	resp, err := m.CIM.PowerManagementService.RequestPowerStateChange(state)
	if err != nil {
		return fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	if rv := resp.Body.RequestPowerStateChangeResponse.ReturnValue; rv != 0 {
		return fmt.Errorf("AMT refused the power request: %s", powerReturn(int(rv)))
	}
	return nil
}

func powerReturn(rv int) string {
	switch rv {
	case 1:
		return "not supported by this firmware"
	case 2:
		return "unknown/unspecified error"
	case 4:
		return "cannot complete in the current power state (it may already be there)"
	case 5:
		return "invalid state transition requested"
	case 6:
		return "timeout"
	case 2049:
		return "access denied — the AMT account lacks the power-administration realm"
	default:
		return fmt.Sprintf("return value %d", rv)
	}
}

func (a *amt) forcePXE(m *wsmanSession) error {
	if err := m.next(); err != nil {
		return err
	}
	if _, err := m.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		a.tracef("boot source clear: %v", describe(err))
	}
	if err := a.putBootSettings(m); err != nil {
		if m.ctx.Err() != nil {
			return m.ctx.Err()
		}
		a.tracef("%v; continuing with the source and role alone", err)
	}
	if err := m.next(); err != nil {
		return err
	}
	role, err := m.CIM.BootService.SetBootConfigRole(bootConfigInstance, 1)
	if err != nil {
		return fmt.Errorf("boot role: %w", describe(err))
	}
	if rv := role.Body.SetBootConfigRole_OUTPUT.ReturnValue; rv != 0 {
		return fmt.Errorf("boot role IsNextSingleUse refused (return value %d)", rv)
	}
	a.tracef("boot role IsNextSingleUse: return 0")
	if err := m.next(); err != nil {
		return err
	}
	order, err := m.CIM.BootConfigSetting.ChangeBootOrder(boot.PXE)
	if err != nil {
		return fmt.Errorf("boot source: %w", describe(err))
	}
	if rv := order.Body.ChangeBootOrder_OUTPUT.ReturnValue; rv != 0 {
		return fmt.Errorf("boot source Force PXE Boot refused (return value %d)", rv)
	}
	a.tracef("boot source Force PXE Boot: return 0")
	return nil
}

const bootConfigInstance = "Intel(r) AMT: Boot Configuration 0"

func (a *amt) putBootSettings(m *wsmanSession) error {
	if err := m.next(); err != nil {
		return err
	}
	cur, err := m.AMT.BootSettingData.Get()
	if err != nil {
		return fmt.Errorf("boot settings: %w", describe(err))
	}
	props, err := bootProperties(cur.XMLOutput)
	if err != nil {
		return fmt.Errorf("boot settings: %w", err)
	}
	a.tracef("boot settings as read: %s", summarize(props))
	err = a.put(m, "mirrored", bootSettingsBody(props))
	if err != nil && strings.Contains(err.Error(), "InvalidRepresentation") {
		err = a.put(m, "base set", bootSettingsBody(baseOnly(props)))
	}
	return err
}

func (a *amt) put(m *wsmanSession, attempt, body string) error {
	if err := m.next(); err != nil {
		return err
	}
	creator := m.AMT.BootSettingData.Base.WSManMessageCreator
	header := creator.CreateHeader("http://schemas.xmlsoap.org/ws/2004/09/transfer/Put", amtboot.AMTBootSettingData, nil, "", "")
	msg := &client.Message{XMLInput: creator.CreateXML(header, body)}
	a.tracef("boot settings put (%s): %s", attempt, body)
	if err := m.AMT.BootSettingData.Base.Execute(msg); err != nil {
		a.tracef("boot settings reply: %s", strings.TrimSpace(msg.XMLOutput))
		return fmt.Errorf("boot settings: %w", describe(err))
	}
	a.tracef("boot settings accepted (%s)", attempt)
	return nil
}

func describe(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "401"):
		return fmt.Errorf("authentication failed (check the MEBx user/password): %w", err)
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("port closed: AMT is not enabled or network access is off in MEBx: %w", err)
	case strings.Contains(s, "no route") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return fmt.Errorf("no answer: wrong address, or the machine has no standby power: %w", err)
	}
	return err
}
