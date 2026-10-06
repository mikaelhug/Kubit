package talos

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTLSRejectionIsOnlyACertificateRefusal(t *testing.T) {
	for msg, want := range map[string]bool{
		`connection error: desc = "error reading server preface: remote error: tls: certificate required"`:          true,
		`connection error: desc = "transport: authentication handshake failed: remote error: tls: bad certificate"`: true,
		`connection error: desc = "transport: authentication handshake failed: context deadline exceeded"`:          false,
	} {
		if got := isTLSRejection(status.Error(codes.Unavailable, msg)); got != want {
			t.Errorf("%s: %v, want %v", msg, got, want)
		}
	}
}
