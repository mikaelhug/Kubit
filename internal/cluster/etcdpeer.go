package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

const (
	etcdClientPort = "2379"
	etcdPeerPort   = "2380"
)

func etcdClientTLS(bundle *secrets.Bundle) (*tls.Config, error) {
	ca, err := x509.NewCertificateAuthorityFromCertificateAndKey(bundle.Certs.Etcd)
	if err != nil {
		return nil, fmt.Errorf("etcd CA: %w", err)
	}
	kp, err := x509.NewKeyPair(ca,
		x509.CommonName("kubit"),
		x509.NotAfter(time.Now().Add(10*time.Minute)),
		x509.KeyUsage(stdx509.KeyUsageDigitalSignature|stdx509.KeyUsageKeyEncipherment),
		x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
		x509.ECDSA(true),
	)
	if err != nil {
		return nil, err
	}
	pem := x509.NewCertificateAndKeyFromKeyPair(kp)
	cert, err := tls.X509KeyPair(pem.Crt, pem.Key)
	if err != nil {
		return nil, err
	}
	roots := stdx509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle.Certs.Etcd.Crt) {
		return nil, fmt.Errorf("etcd CA certificate unreadable")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
}

func peerURL(ip string) string {
	return "https://" + net.JoinHostPort(ip, etcdPeerPort)
}

func etcdMemberUpdate(ctx context.Context, cfg *tls.Config, clientIP string, id uint64, peerURLs []string) error {
	body, err := json.Marshal(map[string]any{"ID": strconv.FormatUint(id, 10), "peerURLs": peerURLs})
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(call, http.MethodPost, "https://"+net.JoinHostPort(clientIP, etcdClientPort)+"/v3/cluster/member/update", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("etcd member update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("etcd member update: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}
