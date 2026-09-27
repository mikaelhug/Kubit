package netx

import (
	"fmt"
	"strconv"
	"strings"
)

func Normalize(mac string) string {
	s := strings.ToLower(strings.TrimSpace(mac))
	var parts []string
	switch {
	case strings.ContainsAny(s, ":-"):
		parts = strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == '-' })
		if strings.Count(s, ":")+strings.Count(s, "-") != 5 {
			return ""
		}
	case len(s) == 12:
		for i := 0; i < 12; i += 2 {
			parts = append(parts, s[i:i+2])
		}
	}
	if len(parts) != 6 {
		return ""
	}
	for i, p := range parts {
		if len(p) > 2 {
			return ""
		}
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return ""
		}
		parts[i] = fmt.Sprintf("%02x", n)
	}
	return strings.Join(parts, ":")
}

func MACKey(mac string) string {
	if n := Normalize(mac); n != "" {
		return n
	}
	return strings.ToLower(mac)
}
