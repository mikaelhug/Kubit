package k8s

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestBareNodeDetailMarshalsEmptyLists(t *testing.T) {
	c := &Client{Interface: fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "a"}}), rest: &rest.Config{Host: "http://127.0.0.1:1"}}
	d, err := c.NodeDetail(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("node detail has null fields: %s", b)
	}
}
