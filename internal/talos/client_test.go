package talos

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestShortGRPCAndHTTPStatus(t *testing.T) {
	err := status.Error(codes.Unavailable, "connection refused")
	if got := ShortGRPC(err).Error(); got != "connection refused (Unavailable)" {
		t.Errorf("short: %q", got)
	}
	if HTTPStatus(err) != 502 || HTTPStatus(status.Error(codes.DeadlineExceeded, "x")) != 504 || HTTPStatus(errors.New("plain")) != 500 {
		t.Error("status mapping")
	}
	if got := ShortGRPC(fmt.Errorf("version: %w", err)).Error(); got != "version: connection refused (Unavailable)" {
		t.Errorf("wrapped: %q", got)
	}
	dial := status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial tcp 10.0.0.1:50000: connect: connection refused"`)
	if got := ShortGRPC(dial).Error(); got != "transport: Error while dialing: dial tcp 10.0.0.1:50000: connect: connection refused (Unavailable)" {
		t.Errorf("dial: %q", got)
	}
	plain := errors.New("plain")
	if ShortGRPC(plain) != plain {
		t.Error("non-gRPC errors pass through")
	}
}
