package config_test

import (
	"sync"
	"testing"
	"time"

	"github.com/mikaelhug/kubit/internal/config"
	talosconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

var sharedBundle = sync.OnceValues(func() (*secrets.Bundle, error) {
	contract, err := talosconfig.ParseContractFromVersion(config.MinTalosVersion)
	if err != nil {
		return nil, err
	}
	return secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
})

func sharedSecrets(t *testing.T) *secrets.Bundle {
	t.Helper()
	b, err := sharedBundle()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const sampleCluster = `
apiVersion: kubit.dev/v1
kind: Cluster
metadata: { name: dev }
spec:
  controlPlane:
    vip: 192.168.64.9
  nodes:
    - { hostname: cp-01, ip: 192.168.64.2, role: controlplane, arch: arm64, mac: "52:54:00:4b:49:01", installDisk: { path: /dev/vda } }
    - { hostname: cp-02, ip: 192.168.64.3, role: controlplane, arch: arm64, installDisk: { selector: { minSize: 10GB, type: virtio } } }
    - { hostname: cp-03, ip: 192.168.64.4, role: controlplane, arch: arm64, installDisk: { path: /dev/vda } }
    - { hostname: worker-01, ip: 192.168.64.5, role: worker, arch: arm64, kvm: true, installDisk: { path: /dev/vda }, dataDisks: [/dev/vdb, /dev/vdc] }
  platform:
    metallb: { enabled: true, range: 192.168.64.200-192.168.64.220 }
    traefik: { enabled: true }
    gvisor: { enabled: true }
    metricsServer: { enabled: true }
`
