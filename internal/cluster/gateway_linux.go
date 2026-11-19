package cluster

import (
	"bufio"
	"encoding/hex"
	"math"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func defaultRoute() (string, string) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	gw, iface, best := "", "", uint64(math.MaxUint64)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 7 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&unix.RTF_UP == 0 {
			continue
		}
		metric, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil || metric >= best {
			continue
		}
		b, err := hex.DecodeString(fields[2])
		if err != nil || len(b) != 4 {
			continue
		}
		gw, iface, best = net.IPv4(b[3], b[2], b[1], b[0]).String(), fields[0], metric
	}
	return gw, iface
}
