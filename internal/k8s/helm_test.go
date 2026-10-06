package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func helmSecret(t *testing.T, revision, status string) *corev1.Secret {
	t.Helper()
	body := `{"name":"metallb","namespace":"metallb-system","version":` + revision + `,"info":{"status":"` + status + `","last_deployed":"2026-10-06T08:00:00Z"},"chart":{"metadata":{"name":"metallb","version":"0.16.1","appVersion":"v0.16.1"}}}`
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(body))
	_ = zw.Close()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.metallb.v" + revision, Namespace: "metallb-system", Labels: map[string]string{"owner": "helm", "name": "metallb", "version": revision}},
		Data:       map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))},
	}
}

func TestHelmReleaseReadsTheLatestRevision(t *testing.T) {
	c := &Client{Interface: fake.NewSimpleClientset(helmSecret(t, "1", "superseded"), helmSecret(t, "10", "failed"), helmSecret(t, "2", "superseded"))}
	r, err := c.HelmRelease(context.Background(), "metallb-system", "metallb")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Revision != 10 || r.Status != "failed" || r.Chart != "metallb" || r.ChartVersion != "0.16.1" || r.AppVersion != "v0.16.1" || r.LastDeployed != 1791273600 {
		t.Fatalf("release: %+v", r)
	}
}
