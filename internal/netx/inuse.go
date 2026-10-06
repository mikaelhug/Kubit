package netx

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

var probePorts = []string{"6443", "50000", "22", "53", "80", "443", "445"}

func InUse(ctx context.Context, ip string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var (
		wg   sync.WaitGroup
		once sync.Once
		used bool
	)
	var d net.Dialer
	for _, port := range probePorts {
		wg.Go(func() {
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if err == nil {
				c.Close()
			}
			if err == nil || errors.Is(err, syscall.ECONNREFUSED) {
				once.Do(func() { used = true; cancel() })
			}
		})
	}
	wg.Wait()
	return used
}
