package netx

import "testing"

func TestNormalizeReadsTheMACFormatsToolsPrint(t *testing.T) {
	cases := map[string]string{
		"52:54:00:6B:09:F3": "52:54:00:6b:09:f3",
		"52-54-00-6b-09-f3": "52:54:00:6b:09:f3",
		"a0:ce:c8:1:2:3":    "a0:ce:c8:01:02:03",
		" 525400ABCDEF ":    "52:54:00:ab:cd:ef",
		"N/A":               "",
		"52:54:00:6b:09":    "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
