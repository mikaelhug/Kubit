package oob

import (
	"encoding/xml"
	"fmt"
	"strings"
)

type property struct {
	Name, Value string
}

func bootProperties(raw string) ([]property, error) {
	d := xml.NewDecoder(strings.NewReader(raw))
	var out []property
	inside := false
	var cur string
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "AMT_BootSettingData" {
				inside = true
				continue
			}
			if inside && cur == "" {
				cur = t.Name.Local
				out = append(out, property{Name: cur})
			}
		case xml.CharData:
			if inside && cur != "" {
				out[len(out)-1].Value += string(t)
			}
		case xml.EndElement:
			if t.Name.Local == "AMT_BootSettingData" {
				inside = false
			} else if inside && t.Name.Local == cur {
				cur = ""
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no AMT_BootSettingData in the reply")
	}
	return out, nil
}

var readOnlyBootProperties = map[string]bool{
	"BIOSLastStatus": true, "BootguardStatus": true, "OptionsCleared": true, "RPEEnabled": true,
	"SecureBootControlEnabled": true, "UEFIHTTPSBootEnabled": true, "UEFILocalPBABootEnabled": true,
	"WinREBootEnabled": true, "UefiBootParametersArray": true, "RSEPassword": true,
}

var baseBootProperties = map[string]bool{
	"BIOSPause": true, "BIOSSetup": true, "BootMediaIndex": true, "ConfigurationDataReset": true,
	"ElementName": true, "EnforceSecureBoot": true, "FirmwareVerbosity": true, "ForcedProgressEvents": true,
	"IDERBootDevice": true, "InstanceID": true, "LockKeyboard": true, "LockPowerButton": true,
	"LockResetButton": true, "LockSleepButton": true, "OwningEntity": true, "ReflashBIOS": true,
	"SecureErase": true, "UseIDER": true, "UseSOL": true, "UseSafeMode": true, "UserPasswordBypass": true,
}

func baseOnly(props []property) []property {
	out := make([]property, 0, len(props))
	for _, p := range props {
		if baseBootProperties[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

var oneShotOff = map[string]string{
	"UseIDER": "false", "IDERBootDevice": "0", "BootMediaIndex": "0", "UseSOL": "false",
	"BIOSPause": "false", "BIOSSetup": "false", "ConfigurationDataReset": "false",
	"ReflashBIOS": "false", "UseSafeMode": "false",
}

func bootSettingsBody(props []property) string {
	var b strings.Builder
	b.WriteString(`<Body><h:AMT_BootSettingData xmlns:h="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_BootSettingData">`)
	for _, p := range props {
		if readOnlyBootProperties[p.Name] {
			continue
		}
		v := strings.TrimSpace(p.Value)
		if o, ok := oneShotOff[p.Name]; ok {
			v = o
		}
		if v == "" {
			continue
		}
		var esc strings.Builder
		_ = xml.EscapeText(&esc, []byte(v))
		fmt.Fprintf(&b, "<h:%s>%s</h:%s>", p.Name, esc.String(), p.Name)
	}
	b.WriteString(`</h:AMT_BootSettingData></Body>`)
	return b.String()
}

func summarize(props []property) string {
	parts := make([]string, 0, len(props))
	for _, p := range props {
		parts = append(parts, p.Name+"="+strings.TrimSpace(p.Value))
	}
	return strings.Join(parts, " ")
}
