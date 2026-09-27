package talos

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func TestTLSRejectionIsOnlyACertificateRefusal(t *testing.T) {
	for msg, want := range map[string]bool{
		`connection error: desc = "error reading server preface: remote error: tls: certificate required"`:          true,
		`connection error: desc = "transport: authentication handshake failed: remote error: tls: bad certificate"`: true,
		`connection error: desc = "transport: authentication handshake failed: context deadline exceeded"`:          false,
		`connection error: desc = "transport: authentication handshake failed: EOF"`:                                false,
		`connection error: desc = "transport: Error while dialing: dial tcp 10.0.0.1:50000: i/o timeout"`:           false,
	} {
		if got := isTLSRejection(status.Error(codes.Unavailable, msg)); got != want {
			t.Errorf("%s: %v, want %v", msg, got, want)
		}
	}
}

func TestInventoryMarshalsEmptyLists(t *testing.T) {
	b, err := json.Marshal(Inventory{IP: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"disks":[]`) || !strings.Contains(string(b), `"links":[]`) {
		t.Errorf("%s", b)
	}
	b, _ = json.Marshal(&Inventory{Disks: []Disk{{DevPath: "/dev/sda"}}})
	if !strings.Contains(string(b), `"devPath":"/dev/sda"`) {
		t.Errorf("pointer marshal: %s", b)
	}
}
