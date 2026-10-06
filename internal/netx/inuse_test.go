package netx

import (
	"context"
	"testing"
	"time"
)

func TestARefusedConnectionMeansTheAddressIsInUse(t *testing.T) {
	ctx := context.Background()
	if !InUse(ctx, "127.0.0.1", 2*time.Second) {
		t.Error("a host that refuses connections is in use")
	}
	if InUse(ctx, "192.0.2.1", 500*time.Millisecond) {
		t.Error("an address nothing answers on is free")
	}
}
