package main

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	u := unit{Label: serviceLabel, Binary: "/usr/local/bin/kubit", Addr: "127.0.0.1:8080", Home: "/home/op/.kubit", Log: "/home/op/.kubit/log/serve.log", User: "op"}
	plist, err := renderUnit("darwin", u, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>dev.kubit.serve</string>", "<string>/usr/local/bin/kubit</string>", "<string>serve</string>", "<key>KUBIT_SERVICE</key><string>1</string>", "<key>KeepAlive</key><true/>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	user, err := renderUnit("linux", u, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(user, "User=") || !strings.Contains(user, "WantedBy=default.target") || !strings.Contains(user, "ExecStart=/usr/local/bin/kubit serve --addr 127.0.0.1:8080") {
		t.Errorf("user unit wrong:\n%s", user)
	}
	sys, err := renderUnit("linux", u, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sys, "User=op") || !strings.Contains(sys, "WantedBy=multi-user.target") {
		t.Errorf("system unit wrong:\n%s", sys)
	}
	if _, err := renderUnit("windows", u, false); err == nil {
		t.Error("windows should be unsupported")
	}
}
