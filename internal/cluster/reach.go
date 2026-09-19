package cluster

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"time"
)

// Reach says whose fault a failed probe is: the target did not answer, or the observer
// itself has no way onto the network (interface down, no route, or the process is not
// allowed to reach the LAN).
type Reach int

const (
	ReachOK Reach = iota
	ReachUnreachable
	ReachNoNetwork
)

const (
	ObserverOnline  = "online"
	ObserverOffline = "offline"
)

var noNetworkErrnos = map[syscall.Errno]bool{syscall.EHOSTUNREACH: true, syscall.ENETUNREACH: true, syscall.EHOSTDOWN: true, syscall.ENETDOWN: true, syscall.EADDRNOTAVAIL: true}

var noNetworkTexts = []string{"no route to host", "network is unreachable", "host is down", "network is down", "can't assign requested address"}

func Classify(err error) Reach {
	if err == nil {
		return ReachOK
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && noNetworkErrnos[errno] {
		return ReachNoNetwork
	}
	msg := strings.ToLower(err.Error())
	for _, t := range noNetworkTexts {
		if strings.Contains(msg, t) {
			return ReachNoNetwork
		}
	}
	return ReachUnreachable
}

// ShortNet keeps the operating system's own words for a dial failure ("no route to
// host") and drops the address the caller already knows.
func ShortNet(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// ControlProbe tells whether the observer can reach its own LAN at all by dialing the
// default gateway: a refusal or a timeout means the network is fine and the targets are
// the problem; a no-network error means the observer is blind. Without a default
// gateway the answer is unknown and the caller must not claim to be blind.
func ControlProbe(timeout time.Duration) (Reach, string) {
	gw := DefaultGateway()
	if gw == "" {
		return ReachOK, ""
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(gw, "443"), timeout)
	if err == nil {
		conn.Close()
		return ReachOK, gw
	}
	if Classify(err) == ReachNoNetwork {
		return ReachNoNetwork, gw
	}
	return ReachOK, gw
}
