package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikael/kubit/internal/cluster"
	"github.com/mikael/kubit/internal/labhost"
	"github.com/mikael/kubit/internal/store"
)

func TestLabMaintainGates(t *testing.T) {
	c, _ := store.NewCrypto(bytes.Repeat([]byte{8}, 32))
	dir := t.TempDir()
	st, err := store.Open(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New("test", cluster.NewManager(st, dir), "", c)
	ctx := t.Context()
	call := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, local(httptest.NewRequest(method, path, nil)))
		return rec
	}
	if rec := call(http.MethodPost, "/api/v1/machines/aa:aa:aa:aa:aa:01/labhost/update"); rec.Code != http.StatusNotFound {
		t.Errorf("update on an unknown machine: %d", rec.Code)
	}
	_ = st.UpsertNode(ctx, store.NodeRow{IP: "10.0.0.9", MAC: "aa:aa:aa:aa:aa:01", Source: "labhost", State: "labhost"})
	_ = st.SetLabHost(ctx, "aa:aa:aa:aa:aa:01", &store.LabHost{State: "updating", Capacity: labhost.Capacity{Hostname: "lab-1"}})
	if rec := call(http.MethodPost, "/api/v1/machines/aa:aa:aa:aa:aa:01/labhost/reboot"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "updating") {
		t.Errorf("reboot while updating: %d %s", rec.Code, rec.Body.String())
	}
	// History is filed under the pseudo-cluster and served from the machine route.
	key := store.LabHostKey("aa:aa:aa:aa:aa:01")
	_ = st.AddSamples(ctx, key, time.Now(), []store.Sample{{CPUMilli: 427, CPUCap: 1000, MemBytes: 12e9, MemCap: 16e9, Pods: 3, Ready: true, Reachable: true, Disk: 180e9, DiskCap: 200e9}})
	rec := call(http.MethodGet, "/api/v1/machines/aa:aa:aa:aa:aa:01/labhost/samples?range=1h")
	var rows []store.Sample
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Disk != 180e9 || rows[0].DiskCap != 200e9 {
		t.Errorf("samples: %d %s", rec.Code, rec.Body.String())
	}
}
