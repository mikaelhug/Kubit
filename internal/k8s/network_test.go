package k8s_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikael/kubit/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSummariesMarshalEmptyListsNotNull(t *testing.T) {
	c := &k8s.Client{Interface: fake.NewClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "a", Name: "headless"}})}
	svcs, err := c.Services(t.Context())
	if err != nil || len(svcs) != 1 {
		t.Fatalf("%v %v", svcs, err)
	}
	b, _ := json.Marshal(svcs[0])
	if !strings.Contains(string(b), `"ports":[]`) {
		t.Errorf("service: %s", b)
	}
	ws, err := c.Workloads(t.Context())
	if b, _ := json.Marshal(ws); err != nil || string(b) != "[]" {
		t.Errorf("no workloads: %s %v", b, err)
	}
}
