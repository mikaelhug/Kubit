package cluster

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/mikael/kubit/internal/talos"
)

func TestClassify(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EHOSTUNREACH}}
	cases := []struct {
		err  error
		want Reach
	}{
		{nil, ReachOK},
		{opErr, ReachNoNetwork},
		{fmt.Errorf("Get \"https://x\": dial tcp 10.0.0.1:6443: connect: no route to host"), ReachNoNetwork},
		{errors.New("dial tcp 10.0.0.1:22: connect: network is unreachable"), ReachNoNetwork},
		{errors.New("dial tcp 10.0.0.1:50000: i/o timeout"), ReachUnreachable},
		{errors.New("connection refused"), ReachUnreachable},
		{errors.New("port 50000 closed or host down"), ReachUnreachable},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if got := ShortNet(opErr); got != "no route to host" {
		t.Errorf("ShortNet = %q", got)
	}
}

func TestEtcdPeerMatchesByPeerURLThenHostname(t *testing.T) {
	ips := map[string]bool{"10.0.0.5": true}
	cases := []struct {
		p    talos.EtcdPeer
		want bool
	}{
		{talos.EtcdPeer{Hostname: "old-name", PeerURLs: []string{"https://10.0.0.5:2380"}}, true},
		{talos.EtcdPeer{Hostname: "cp-2", PeerURLs: []string{"https://10.0.0.6:2380"}}, true},
		{talos.EtcdPeer{Hostname: "cp-3", PeerURLs: []string{"https://10.0.0.7:2380"}}, false},
	}
	for _, c := range cases {
		if got := peerMatches(c.p, "cp-2", ips); got != c.want {
			t.Errorf("%+v: %v", c.p, got)
		}
	}
}
