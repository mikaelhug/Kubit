package cluster

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
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
