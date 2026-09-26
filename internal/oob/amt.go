package oob

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman"
	amtboot "github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/amt/boot"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/boot"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/cim/power"
	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/wsman/client"
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
		return &amt{c: c, trace: o.trace}, nil
	case "redfish":
		if c.Host == "" || c.User == "" || c.Password == "" {
			return nil, errors.New("Redfish needs the BMC address, user and password")
		}
		return newRedfish(c, o.trace), nil
	}
	return nil, errors.New("no out-of-band management configured")
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

type amt struct {
	c     Config
	trace func(string)
}

func (a *amt) tracef(format string, args ...any) {
	if a.trace != nil {
		a.trace(fmt.Sprintf(format, args...))
	}
}

func (a *amt) msgs(ctx context.Context) wsman.Messages {
	timeout := 15 * time.Second
	if d, ok := ctx.Deadline(); ok {
		if rem := time.Until(d); rem > 0 && rem < timeout {
			timeout = rem
		}
	}
	return wsman.NewMessages(client.Parameters{
		Target: a.c.Host, Username: a.c.User, Password: a.c.Password,
		UseDigest: true, UseTLS: a.c.TLS, SelfSignedAllowed: true, Timeout: timeout,
	})
}

func (a *amt) Probe(ctx context.Context) (Info, error) {
	m := a.msgs(ctx)
	var info Info
	sw, err := m.CIM.SoftwareIdentity.Enumerate()
	if err != nil {
		return info, fmt.Errorf("AMT at %s: %w", a.c.Host, describe(err))
	}
	if p, err := m.CIM.SoftwareIdentity.Pull(sw.Body.EnumerateResponse.EnumerationContext); err == nil {
		for _, s := range p.Body.PullResponse.SoftwareIdentityItems {
			if s.InstanceID == "AMT" {
				info.Version = s.VersionString
			}
		}
	}
	if e, err := m.AMT.EthernetPortSettings.Enumerate(); err == nil {
		if p, err := m.AMT.EthernetPortSettings.Pull(e.Body.EnumerateResponse.EnumerationContext); err == nil {
			for _, port := range p.Body.PullResponse.EthernetPortItems {
				if port.MACAddress != "" && !strings.Contains(strings.ToLower(port.InstanceID), "wireless") {
					info.MAC = strings.ToLower(strings.ReplaceAll(port.MACAddress, "-", ":"))
					break
				}
			}
		}
	}
	if e, err := m.CIM.Chassis.Enumerate(); err == nil {
		if p, err := m.CIM.Chassis.Pull(e.Body.EnumerateResponse.EnumerationContext); err == nil && len(p.Body.PullResponse.PackageItems) > 0 {
			ch := p.Body.PullResponse.PackageItems[0]
			info.Manufacturer, info.Model, info.Serial = ch.Manufacturer, ch.Model, ch.SerialNumber
		}
	}
	info.Power = a.powerState(m)
	return info, nil
}

func (a *amt) powerState(m wsman.Messages) string {
	e, err := m.CIM.AssociatedPowerManagementService.Enumerate()
	if err != nil {
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
	m := a.msgs(ctx)
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

func (a *amt) forcePXE(m wsman.Messages) error {
	if _, err := m.CIM.BootConfigSetting.ChangeBootOrder(""); err != nil {
		a.tracef("boot source clear: %v", describe(err))
	}
	if err := a.putBootSettings(m); err != nil {
		a.tracef("%v; continuing with the source and role alone", err)
	}
	role, err := m.CIM.BootService.SetBootConfigRole(bootConfigInstance, 1)
	if err != nil {
		return fmt.Errorf("boot role: %w", describe(err))
	}
	if rv := role.Body.SetBootConfigRole_OUTPUT.ReturnValue; rv != 0 {
		return fmt.Errorf("boot role IsNextSingleUse refused (return value %d)", rv)
	}
	a.tracef("boot role IsNextSingleUse: return 0")
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

func (a *amt) putBootSettings(m wsman.Messages) error {
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

func (a *amt) put(m wsman.Messages, attempt, body string) error {
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
