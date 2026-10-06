//go:build integration

package pxe

import (
	"context"
	"os"
	"testing"
)

func TestUpstreamIPXEBinariesStillDownload(t *testing.T) {
	c := NewCache(t.TempDir())
	for _, name := range []string{FileBIOS, FileX64, FileARM64} {
		p, err := c.IPXEBinary(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		st, _ := os.Stat(p)
		if st.Size() < 50_000 {
			t.Errorf("%s: %d bytes looks wrong", name, st.Size())
		}
	}
}
