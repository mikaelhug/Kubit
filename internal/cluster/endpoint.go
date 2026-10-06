package cluster

import "k8s.io/client-go/tools/clientcmd"

func kubeconfigAt(kubeconfig []byte, endpoint string) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, err
	}
	for _, c := range cfg.Clusters {
		c.Server = endpoint
	}
	return clientcmd.Write(*cfg)
}

func (m *Manager) followEndpoint(d *Desired, live string) []byte {
	endpoint := live
	if endpoint == "" {
		endpoint = d.Cluster.Spec.ControlPlane.Endpoint
	}
	if kc, err := kubeconfigAt(d.Kubeconfig, endpoint); err == nil {
		return kc
	}
	return d.Kubeconfig
}

func (m *Manager) liveKubeconfig(d *Desired) []byte {
	m.desiredMu.Lock()
	live := m.liveEndpoint[d.Cluster.Metadata.Name]
	m.desiredMu.Unlock()
	return m.followEndpoint(d, live)
}

func (m *Manager) pinEndpoint(name, live string) {
	m.desiredMu.Lock()
	if m.liveEndpoint == nil {
		m.liveEndpoint = map[string]string{}
	}
	m.liveEndpoint[name] = live
	d := m.desired[name]
	m.desiredMu.Unlock()
	if d != nil {
		m.storeSecrets(d)
	}
}

func (m *Manager) storeSecrets(d *Desired) {
	m.desiredMu.Lock()
	live := m.liveEndpoint[d.Cluster.Metadata.Name]
	m.desiredMu.Unlock()
	m.Store.PutClusterSecrets(d.Cluster.Metadata.Name, storeSecretsOf(d.Talosconfig, m.followEndpoint(d, live)))
}
