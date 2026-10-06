package cluster

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"time"

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
	sec, bundle, err := m.loadSecrets(name)
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
