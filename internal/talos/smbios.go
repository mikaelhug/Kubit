package talos

import (
	"bytes"
	"context"
	"encoding/binary"
	"slices"
	"strings"
)

type MemoryModule struct {
	Slot          string `json:"slot"`
	Bank          string `json:"bank,omitempty"`
	SizeBytes     uint64 `json:"sizeBytes"`
	Empty         bool   `json:"empty,omitempty"`
	Type          string `json:"type,omitempty"`
	SpeedMTs      uint32 `json:"speedMTs,omitempty"`
	ConfiguredMTs uint32 `json:"configuredMTs,omitempty"`
	Manufacturer  string `json:"manufacturer,omitempty"`
	Part          string `json:"part,omitempty"`
	Serial        string `json:"serial,omitempty"`
}

const dmiTable = "/sys/firmware/dmi/tables/DMI"

// SMBIOS memory device types, DMTF DSP0134 7.18.2.
var memoryTypes = map[byte]string{
	0x12: "DDR", 0x13: "DDR2", 0x18: "DDR3", 0x1a: "DDR4", 0x1b: "LPDDR", 0x1c: "LPDDR2", 0x1d: "LPDDR3",
	0x1e: "LPDDR4", 0x20: "HBM", 0x21: "HBM2", 0x22: "DDR5", 0x23: "LPDDR5", 0x24: "HBM3",
}

var dmiPlaceholders = []string{"to be filled by o.e.m.", "default string", "not specified", "unknown", "none"}

func dmiText(s string) string {
	s = strings.TrimSpace(s)
	if slices.Contains(dmiPlaceholders, strings.ToLower(s)) {
		return ""
	}
	return s
}

func (c *Client) memoryModules(ctx context.Context) []MemoryModule {
	b, err := c.readAll(ctx, dmiTable)
	if err != nil {
		return nil
	}
	return decodeMemoryDevices(b)
}

// Offsets follow the SMBIOS memory device (type 17) layout, DMTF DSP0134 7.18.
func decodeMemoryDevices(b []byte) []MemoryModule {
	var out []MemoryModule
	for len(b) >= 4 {
		typ, n := b[0], int(b[1])
		if typ == 127 || n < 4 || n > len(b) {
			break
		}
		end := bytes.Index(b[n:], []byte{0, 0})
		if end < 0 {
			break
		}
		f, set := b[:n], b[n:n+end]
		b = b[n+end+2:]
		if typ != 17 || n < 0x1b {
			continue
		}
		strs := strings.Split(string(set), "\x00")
		str := func(off int) string {
			if i := int(f[off]); i > 0 && i <= len(strs) {
				return dmiText(strs[i-1])
			}
			return ""
		}
		speed := func(off int) uint32 {
			if v := binary.LittleEndian.Uint16(f[off:]); v != 0xffff {
				return uint32(v)
			}
			return 0
		}
		m := MemoryModule{Slot: str(0x10), Bank: str(0x11)}
		switch size := binary.LittleEndian.Uint16(f[0x0c:]); {
		case size == 0:
			out = append(out, MemoryModule{Slot: m.Slot, Bank: m.Bank, Empty: true})
			continue
		case size == 0x7fff && n >= 0x20:
			m.SizeBytes = uint64(binary.LittleEndian.Uint32(f[0x1c:])&0x7fffffff) << 20
		case size == 0xffff || size == 0x7fff:
		case size&0x8000 != 0:
			m.SizeBytes = uint64(size&0x7fff) << 10
		default:
			m.SizeBytes = uint64(size) << 20
		}
		m.Type = memoryTypes[f[0x12]]
		m.SpeedMTs = speed(0x15)
		m.Manufacturer, m.Serial, m.Part = str(0x17), str(0x18), str(0x1a)
		if n >= 0x22 {
			m.ConfiguredMTs = speed(0x20)
		}
		out = append(out, m)
	}
	return out
}
