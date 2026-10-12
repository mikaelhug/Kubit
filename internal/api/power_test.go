package api

import (
	"net/http"
	"strings"
	"testing"
)

const threeControlPlanes = "apiVersion: kubit.dev/v1\nkind: Cluster\nmetadata: {name: lab}\nspec:\n  nodes:\n" +
	"    - {hostname: cp-01, ip: 10.0.0.1, role: controlplane, installDisk: {path: /dev/sda}}\n" +
	"    - {hostname: cp-02, ip: 10.0.0.2, role: controlplane, installDisk: {path: /dev/sda}}\n" +
	"    - {hostname: cp-03, ip: 10.0.0.3, role: controlplane, installDisk: {path: /dev/sda}}\n" +
	"    - {hostname: w-01, ip: 10.0.0.4, installDisk: {path: /dev/sda}}\n"

func TestShutdownThatBreaksEtcdOrTheEndpointIsRefusedAndStartsNothing(t *testing.T) {
	s, _ := servedSpec(t, threeControlPlanes)
	for body, why := range map[string]string{
		`{"action":"shutdown","hostnames":["cp-02","cp-03"]}`: "etcd",
		`{"action":"shutdown","hostnames":["cp-01"]}`:         "API endpoint",
	} {
		rec := call(t, s, http.MethodPost, "/api/v1/clusters/lab/nodes/power", body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), why) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
		if s.applying("lab") {
			t.Fatalf("%s started a run", body)
		}
	}
}
