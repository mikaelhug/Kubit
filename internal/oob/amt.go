// Package oob is out-of-band management: reaching a machine's management engine
// when the OS is absent, dead or powered off. Intel AMT (vPro) is the first backend;
// it gives Kubit power control and "boot from network once", which turns first
// contact and re-provisioning into a click instead of a walk to the machine.
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

// Config is how to reach one machine's AMT; the password is sealed by the store.
type Config struct {
	Type     string `json:"type"` // "" (none) | amt
	Host     string `json:"host"` // AMT shares the wired NIC's address
	User     string `json:"user"` // usually "admin"
	Password string `json:"password"`
	TLS      bool   `json:"tls"` // 16993 with TLS, 16992 without
}

func (c Config) Enabled() bool { return c.Type == "amt" && c.Host != "" }

// Info is what a probe learns: enough to create the machine row before Talos ever
// booted, and the power state the Inventory shows.
type Info struct {
	Version      string `json:"version"`
	MAC          string `json:"mac"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Power        string `json:"power"` // on | off | sleep | unknown
}

// Action is a power request.
type Action string

const (
	PowerOn    Action = "on"
	PowerOff   Action = "off"
	Reset      Action = "reset"
	PowerCycle Action = "cycle"
	// BootPXE forces one network boot on the next start (then on/reset as needed).
	BootPXE Action = "pxe"
)

// Manager abstracts the backend so the API and tests do not depend on WS-Man.
type Manager interface {
	Probe(ctx context.Context) (Info, error)
	Power(ctx context.Context, a Action) error
}

// Option tunes a backend; WithTrace receives one line per boot-related request and
// reply so an operation log shows what AMT was asked and answered.
type Option func(*amt)

func WithTrace(fn func(string)) Option { return func(a *amt) { a.trace = fn } }

// Open returns the backend for a config.
func Open(c Config, opts ...Option) (Manager, error) {
	switch c.Type {
	case "amt":
		if c.Host == "" || c.User == "" || c.Password == "" {
			return nil, errors.New("AMT needs host, user and password")
		}
		a := &amt{c: c}
		for _, o := range opts {
			o(a)
		}
		return a, nil
	}
	return nil, errors.New("no out-of-band management configured")
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
	if d, ok := ctx.Deadline(); ok && time.Until(d) < timeout {
		timeout = time.Until(d)
	}
	return wsman.NewMessages(client.Parameters{
		Target: a.c.Host, Username: a.c.User, Password: a.c.Password,
		UseDigest: true, UseTLS: a.c.TLS, SelfSignedAllowed: true, Timeout: timeout,
	})
}

func (a *amt) Probe(ctx context.Context) (Info, error) {
	m := a.msgs(ctx)
	var info Info
	// Software identity carries the AMT version; it is also the cheapest "are the
	// credentials right" call.
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
		// A powered-off box needs "on"; a running one a reset. Either way one boot.
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
		return fmt.Errorf("AMT refused the power request (return value %d); the machine may already be in that state", rv)
	}
	return nil
}

// forcePXE arms one network boot the way Intel's console does: clear the boot
// source, write the boot settings with every one-shot option off, give the boot
// configuration the IsNextSingleUse role (without it the BIOS ignores the source),
// set the PXE source. Every step's reply is traced.
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

// bootConfigInstance is the one CIM_BootConfigSetting AMT has (AMT 6.0+).
const bootConfigInstance = "Intel(r) AMT: Boot Configuration 0"

// putBootSettings writes AMT_BootSettingData back the way the firmware reported it,
// one-shot options switched off. A fixed property set is refused by AMT 11/12 as
// InvalidRepresentation; a refused mirrored write is retried with the AMT 11 base set.
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

// describe turns the library's transport errors into something an operator can act on.
func describe(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "401"):
		return errors.New("authentication failed (check the MEBx user/password)")
	case strings.Contains(s, "connection refused"):
		return errors.New("port closed: AMT is not enabled or network access is off in MEBx")
	case strings.Contains(s, "no route") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return errors.New("no answer: wrong address, or the machine has no standby power")
	}
	return err
}
