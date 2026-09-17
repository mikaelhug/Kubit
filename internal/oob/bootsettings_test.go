package oob

import (
	"strings"
	"testing"
)

const amt12Get = `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://intel.com/wbem/wscim/1/amt-schema/1/AMT_BootSettingData"><a:Header/><a:Body><g:AMT_BootSettingData><g:BIOSLastStatus>2</g:BIOSLastStatus><g:BIOSLastStatus>0</g:BIOSLastStatus><g:BIOSPause>false</g:BIOSPause><g:BIOSSetup>false</g:BIOSSetup><g:BootMediaIndex>0</g:BootMediaIndex><g:BootguardStatus>0</g:BootguardStatus><g:ConfigurationDataReset>false</g:ConfigurationDataReset><g:ElementName>Intel(r) AMT Boot Configuration Settings</g:ElementName><g:EnforceSecureBoot>false</g:EnforceSecureBoot><g:FirmwareVerbosity>0</g:FirmwareVerbosity><g:ForcedProgressEvents>false</g:ForcedProgressEvents><g:IDERBootDevice>0</g:IDERBootDevice><g:InstanceID>Intel(r) AMT:BootSettingData 0</g:InstanceID><g:LockKeyboard>false</g:LockKeyboard><g:LockPowerButton>false</g:LockPowerButton><g:LockResetButton>false</g:LockResetButton><g:LockSleepButton>false</g:LockSleepButton><g:OptionsCleared>true</g:OptionsCleared><g:OwningEntity>Intel(r) AMT</g:OwningEntity><g:ReflashBIOS>false</g:ReflashBIOS><g:SecureErase>false</g:SecureErase><g:UefiBootNumberOfParams>0</g:UefiBootNumberOfParams><g:UefiBootParametersArray></g:UefiBootParametersArray><g:UseIDER>false</g:UseIDER><g:UseSOL>false</g:UseSOL><g:UseSafeMode>false</g:UseSafeMode><g:UserPasswordBypass>false</g:UserPasswordBypass><g:SomethingNewer>7</g:SomethingNewer></g:AMT_BootSettingData></a:Body></a:Envelope>`

func TestBootSettingsMirror(t *testing.T) {
	props, err := bootProperties(amt12Get)
	if err != nil {
		t.Fatal(err)
	}
	if props[0].Name != "BIOSLastStatus" || props[len(props)-1].Name != "SomethingNewer" || len(props) != 28 {
		t.Fatalf("walker: %d props, first %s last %s", len(props), props[0].Name, props[len(props)-1].Name)
	}
	body := bootSettingsBody(props)
	for _, ro := range []string{"BIOSLastStatus", "BootguardStatus", "OptionsCleared", "UefiBootParametersArray"} {
		if strings.Contains(body, "<h:"+ro+">") {
			t.Errorf("read-only %s must not be sent", ro)
		}
	}
	for _, want := range []string{"<h:UseIDER>false</h:UseIDER>", "<h:IDERBootDevice>0</h:IDERBootDevice>", "<h:UefiBootNumberOfParams>0</h:UefiBootNumberOfParams>", "<h:SomethingNewer>7</h:SomethingNewer>", "<h:ElementName>Intel(r) AMT Boot Configuration Settings</h:ElementName>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s", want)
		}
	}
	if strings.Index(body, "<h:BIOSPause>") > strings.Index(body, "<h:BootMediaIndex>") || strings.Index(body, "<h:UseIDER>") > strings.Index(body, "<h:UseSOL>") {
		t.Error("order must follow the Get reply")
	}
	if !strings.Contains(bootSettingsBody([]property{{Name: "ElementName", Value: "a<b&c"}}), "a&lt;b&amp;c") {
		t.Error("text must be escaped")
	}
	b := bootSettingsBody([]property{{Name: "PlatformErase"}, {Name: "UseIDER"}})
	if strings.Contains(b, "PlatformErase") || !strings.Contains(b, "<h:UseIDER>false</h:UseIDER>") {
		t.Errorf("empty values are omitted unless a one-shot option: %s", b)
	}
	if strings.Contains(bootSettingsBody([]property{{Name: "ElementName", Value: "x"}}), "UseIDER") {
		t.Error("properties the firmware did not list are not invented")
	}
}

func TestBootSettingsBaseSet(t *testing.T) {
	props, _ := bootProperties(amt12Get)
	base := baseOnly(props)
	if len(base) != 21 || base[0].Name != "BIOSPause" || base[len(base)-1].Name != "UserPasswordBypass" {
		t.Fatalf("base set: %d props, first %s last %s", len(base), base[0].Name, base[len(base)-1].Name)
	}
	body := bootSettingsBody(base)
	for _, extra := range []string{"UefiBootNumberOfParams", "SomethingNewer", "BIOSLastStatus"} {
		if strings.Contains(body, extra) {
			t.Errorf("base body must not carry %s", extra)
		}
	}
	if strings.Count(body, "<h:") != 22 {
		t.Errorf("base body has %d elements, want class + 21", strings.Count(body, "<h:"))
	}
}
