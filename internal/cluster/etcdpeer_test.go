package cluster

import (
	"context"
	"crypto/tls"
	stdx509 "crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/crypto/x509"
)

func TestEtcdGatewayMemberUpdateTakesAStringIDAndTheClusterCA(t *testing.T) {
	b := bundle(t)
	ca, err := x509.NewCertificateAuthorityFromCertificateAndKey(b.Certs.Etcd)
	if err != nil {
		t.Fatal(err)
	}
	server, err := x509.NewKeyPair(ca, x509.CommonName("etcd"), x509.IPAddresses([]net.IP{net.ParseIP("127.0.0.1")}), x509.NotAfter(time.Now().Add(time.Hour)), x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageServerAuth}), x509.ECDSA(true))
	if err != nil {
		t.Fatal(err)
	}
	pem := x509.NewCertificateAndKeyFromKeyPair(server)
	cert, err := tls.X509KeyPair(pem.Crt, pem.Key)
	if err != nil {
		t.Fatal(err)
	}
	clients := stdx509.NewCertPool()
	clients.AppendCertsFromPEM(b.Certs.Etcd.Crt)
	var got map[string]any
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/cluster/member/update" || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: clients, ClientAuth: tls.RequireAndVerifyClientCert}
	l, err := net.Listen("tcp", "127.0.0.1:"+etcdClientPort)
	if err != nil {
		t.Skipf("port %s is taken: %v", etcdClientPort, err)
	}
	srv.Listener = l
	srv.StartTLS()
	defer srv.Close()
	cfg, err := etcdClientTLS(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := etcdMemberUpdate(context.Background(), cfg, "127.0.0.1", 18446744073709551615, []string{peerURL("192.168.5.51")}); err != nil {
		t.Fatal(err)
	}
	if got["ID"] != "18446744073709551615" || !strings.Contains(strings.Join(toStrings(got["peerURLs"]), ","), "https://192.168.5.51:2380") {
		t.Errorf("request %v", got)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
