package netx

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"52:54:00:6B:09:F3":    "52:54:00:6b:09:f3",
		"52-54-00-6b-09-f3":    "52:54:00:6b:09:f3",
		"a0:ce:c8:1:2:3":       "a0:ce:c8:01:02:03",
		"0-e-3c-c5-4b-d1":      "00:0e:3c:c5:4b:d1",
		" 525400ABCDEF ":       "52:54:00:ab:cd:ef",
		"":                     "",
		"N/A":                  "",
		"52:54:00:6b:09":       "",
		"52:54:00:6b:09:f3:01": "",
		"52:54:00:6b:09:fff":   "",
		"52:54:00:6b:09:zz":    "",
		"52::00:6b:09:f3":      "",
		"52:54-00:6b:09:f3":    "52:54:00:6b:09:f3",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMACKey(t *testing.T) {
	cases := map[string]string{
		"52-54-00-6B-09-F3": "52:54:00:6b:09:f3",
		"525400ABCDEF":      "52:54:00:ab:cd:ef",
		"Unknown@10.0.0.9":  "unknown@10.0.0.9",
		"":                  "",
	}
	for in, want := range cases {
		if got := MACKey(in); got != want {
			t.Errorf("MACKey(%q) = %q, want %q", in, got, want)
		}
	}
}
