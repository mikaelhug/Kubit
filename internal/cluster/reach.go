package cluster

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"time"

	"github.com/mikael/kubit/internal/talos"
)

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

func ShortNet(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

func ControlProbe(ctx context.Context, timeout time.Duration) Reach {
	gw := DefaultGateway()
	if gw == "" {
		return ReachOK
	}
	err := talos.TCPErr(ctx, gw, "443", timeout)
	if err == nil {
		return ReachOK
	}
	if Classify(err) == ReachNoNetwork {
		return ReachNoNetwork
	}
	return ReachOK
}
