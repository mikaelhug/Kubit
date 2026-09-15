package cluster

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/mikael/kubit/internal/config"
	"github.com/mikael/kubit/internal/talos"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"go.yaml.in/yaml/v4"
)

// CertInfo describes one credential Kubit holds for a cluster and when it stops working.
type CertInfo struct {
	Name      string    `json:"name"` // talosconfig | kubeconfig | talos-ca | kubernetes-ca | etcd-ca | aggregator-ca
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer,omitempty"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	DaysLeft  int       `json:"daysLeft"`
	Rotatable bool      `json:"rotatable"` // Kubit can mint a replacement through the Talos API
	Error     string    `json:"error,omitempty"`
}

// Certificates parses the stored talosconfig, kubeconfig and CA certificates.
func (m *Manager) Certificates(ctx context.Context, name string) ([]CertInfo, error) {
	sec, bundle, err := m.loadSecrets(ctx, name)
	if err != nil {
		return nil, err
	}
	var out []CertInfo
	add := func(n string, der []byte, rotatable bool, perr error) {
		ci := CertInfo{Name: n, Rotatable: rotatable}
		if perr != nil {
			ci.Error = perr.Error()
			out = append(out, ci)
			return
		}
		crt, err := x509.ParseCertificate(der)
		if err != nil {
			ci.Error = err.Error()
			out = append(out, ci)
			return
		}
		ci.Subject, ci.Issuer, ci.NotBefore, ci.NotAfter = crt.Subject.String(), crt.Issuer.String(), crt.NotBefore, crt.NotAfter
		ci.DaysLeft = int(time.Until(crt.NotAfter).Hours() / 24)
		out = append(out, ci)
	}
	tcfg, err := clientconfig.FromBytes(sec.Talosconfig)
	if err == nil {
		if c, ok := tcfg.Contexts[tcfg.Context]; ok {
			der, derr := derFromBase64PEM(c.Crt)
			add("talosconfig", der, true, derr)
		} else {
			add("talosconfig", nil, true, fmt.Errorf("context %q missing", tcfg.Context))
		}
	} else {
		add("talosconfig", nil, true, err)
	}
	if sec.Kubeconfig != nil {
		der, derr := kubeconfigClientCert(sec.Kubeconfig)
		add("kubeconfig", der, true, derr)
	}
	pemCA := func(n string, p []byte) {
		der, derr := derFromPEM(p)
		add(n, der, false, derr)
	}
	if bundle != nil && bundle.Certs != nil {
		if bundle.Certs.OS != nil {
			pemCA("talos-ca", bundle.Certs.OS.Crt)
		}
		if bundle.Certs.K8s != nil {
			pemCA("kubernetes-ca", bundle.Certs.K8s.Crt)
		}
		if bundle.Certs.Etcd != nil {
			pemCA("etcd-ca", bundle.Certs.Etcd.Crt)
		}
		if bundle.Certs.K8sAggregator != nil {
			pemCA("aggregator-ca", bundle.Certs.K8sAggregator.Crt)
		}
	}
	return out, nil
}

func derFromPEM(p []byte) ([]byte, error) {
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, fmt.Errorf("not PEM")
	}
	return block.Bytes, nil
}

func derFromBase64PEM(b64 string) ([]byte, error) {
	p, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	return derFromPEM(p)
}

// kubeconfigClientCert returns the DER client certificate of the kubeconfig's first user.
func kubeconfigClientCert(kubeconfig []byte) ([]byte, error) {
	var kc struct {
		Users []struct {
			User struct {
				ClientCertificateData string `yaml:"client-certificate-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(kubeconfig, &kc); err != nil {
		return nil, err
	}
	if len(kc.Users) == 0 || kc.Users[0].User.ClientCertificateData == "" {
		return nil, fmt.Errorf("kubeconfig has no client certificate")
	}
	return derFromBase64PEM(kc.Users[0].User.ClientCertificateData)
}

// RotateCredential mints a fresh admin talosconfig or kubeconfig through the Talos API
// (valid one year) and replaces the stored copy. The old one keeps working until it
// expires; nothing on the nodes changes.
func (m *Manager) RotateCredential(ctx context.Context, name, which string, sink Sink) error {
	sink.plan(Steps("rotate", "Issue a new "+which, "store", "Replace the stored copy")...)
	c, _, err := m.LoadCluster(ctx, name)
	if err != nil {
		return err
	}
	sec, err := m.Store.GetClusterSecrets(ctx, name)
	if err != nil {
		return err
	}
	cps := c.ControlPlanes()
	if len(cps) == 0 {
		return fmt.Errorf("no control planes")
	}
	var fresh []byte
	err = sink.run("rotate", func() error {
		var last error
		for _, cp := range cps {
			dial, cancel := context.WithTimeout(ctx, 15*time.Second)
			tc, err := talos.Dial(dial, cp.IP, sec.Talosconfig)
			cancel()
			if err != nil {
				last = err
				continue
			}
			call, cancel := context.WithTimeout(ctx, 60*time.Second)
			switch which {
			case "talosconfig":
				fresh, err = tc.GenerateTalosconfig(call, 365*24*time.Hour)
			case "kubeconfig":
				fresh, err = tc.Kubeconfig(tc.Context(call))
			default:
				err = fmt.Errorf("unknown credential %q (talosconfig | kubeconfig)", which)
			}
			cancel()
			tc.Close()
			if err == nil {
				sink.emit(Info, "rotate", cp.Hostname, "new %s issued, valid one year", which)
				return nil
			}
			last = err
			if which != "talosconfig" && which != "kubeconfig" {
				return err
			}
		}
		return last
	})
	if err != nil {
		return err
	}
	return sink.run("store", func() error {
		switch which {
		case "talosconfig":
			fresh = rewriteTalosconfigEndpoints(fresh, c)
			if err := m.Store.SetTalosconfig(ctx, name, fresh); err != nil {
				return err
			}
		case "kubeconfig":
			if err := m.Store.SetKubeconfig(ctx, name, fresh); err != nil {
				return err
			}
		}
		_ = m.Store.Audit(ctx, name, "cert.rotate", which)
		sink.emit(Done, "store", "", "%s replaced; export or download it again where it is used outside Kubit", which)
		return nil
	})
}

// rewriteTalosconfigEndpoints keeps Kubit's endpoint list (all control planes) on a
// talosconfig generated by a single node, which only names itself.
func rewriteTalosconfigEndpoints(tc []byte, c *config.Cluster) []byte {
	cfg, err := clientconfig.FromBytes(tc)
	if err != nil {
		return tc
	}
	ctxc, ok := cfg.Contexts[cfg.Context]
	if !ok {
		return tc
	}
	var eps []string
	for _, cp := range c.ControlPlanes() {
		eps = append(eps, cp.IP)
	}
	ctxc.Endpoints = eps
	ctxc.Nodes = nil
	if name := c.Metadata.Name; name != "" && cfg.Context != name {
		cfg.Contexts[name] = ctxc
		delete(cfg.Contexts, cfg.Context)
		cfg.Context = name
	}
	out, err := cfg.Bytes()
	if err != nil {
		return tc
	}
	return out
}
