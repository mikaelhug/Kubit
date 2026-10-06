package repo

import (
	stdx509 "crypto/x509"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	"go.yaml.in/yaml/v4"
)

const (
	credentialTTL = 365 * 24 * time.Hour
	ClockSkew     = 24 * time.Hour
)

func (r *Repo) Talosconfig() ([]byte, error) {
	cert, err := secrets.NewAdminCertificateAndKey(time.Now().Add(-ClockSkew), r.Secrets.Bundle.Certs.OS, role.MakeSet(role.Admin), credentialTTL+ClockSkew)
	if err != nil {
		return nil, err
	}
	var endpoints []string
	for _, n := range r.Cluster.ControlPlanes() {
		endpoints = append(endpoints, n.TargetIP())
	}
	cfg := config.NewConfig(r.Cluster.Metadata.Name, endpoints, r.Secrets.Bundle.Certs.OS.Crt, cert)
	return cfg.Bytes()
}

func (r *Repo) Kubeconfig() ([]byte, error) {
	ca, err := x509.NewCertificateAuthorityFromCertificateAndKey(r.Secrets.Bundle.Certs.K8s)
	if err != nil {
		return nil, fmt.Errorf("kubernetes CA: %w", err)
	}
	kp, err := x509.NewKeyPair(ca,
		x509.CommonName("admin"),
		x509.Organization("system:masters"),
		x509.NotBefore(time.Now().Add(-ClockSkew)),
		x509.NotAfter(time.Now().Add(credentialTTL)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature|stdx509.KeyUsageKeyEncipherment),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
		x509.ECDSA(true),
	)
	if err != nil {
		return nil, err
	}
	pem := x509.NewCertificateAndKeyFromKeyPair(kp)
	name := r.Cluster.Metadata.Name
	user := "admin@" + name
	b64 := base64.StdEncoding.EncodeToString
	kc := map[string]any{
		"apiVersion":      "v1",
		"kind":            "Config",
		"clusters":        []any{map[string]any{"name": name, "cluster": map[string]any{"server": r.Cluster.Spec.ControlPlane.Endpoint, "certificate-authority-data": b64(r.Secrets.Bundle.Certs.K8s.Crt)}}},
		"users":           []any{map[string]any{"name": user, "user": map[string]any{"client-certificate-data": b64(pem.Crt), "client-key-data": b64(pem.Key)}}},
		"contexts":        []any{map[string]any{"name": user, "context": map[string]any{"cluster": name, "user": user, "namespace": "default"}}},
		"current-context": user,
	}
	return yaml.Marshal(kc)
}
