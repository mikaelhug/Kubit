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

type CertInfo struct {
	Name      string    `json:"name"`
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer,omitempty"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	DaysLeft  int       `json:"daysLeft"`
	Rotatable bool      `json:"rotatable"`
	Error     string    `json:"error,omitempty"`
}

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

func (m *Manager) RotateCredential(ctx context.Context, name, which string, sink Sink) error {
	sink.Plan(Steps("rotate", "Issue a new "+which, "store", "Replace the stored copy")...)
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
	err = sink.Run("rotate", func() error {
		if which != "talosconfig" && which != "kubeconfig" {
			return fmt.Errorf("unknown credential %q (talosconfig | kubeconfig)", which)
		}
		cp, tc, err := firstControlPlane(ctx, cps, sec.Talosconfig, func(_ config.Node, tc *talos.Client) error {
			var err error
			if which == "talosconfig" {
				fresh, err = generateTalosconfig(ctx, tc, 365*24*time.Hour)
			} else {
				fresh, err = adminKubeconfig(ctx, tc, credentialTimeout)
			}
			return err
		})
		if err != nil {
			return err
		}
		tc.Close()
		sink.Emit(Info, "rotate", cp.Hostname, "new %s issued, valid one year", which)
		return nil
	})
	if err != nil {
		return err
	}
	return sink.Run("store", func() error {
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
		sink.Emit(Done, "store", "", "%s replaced; export or download it again where it is used outside Kubit", which)
		return nil
	})
}

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
