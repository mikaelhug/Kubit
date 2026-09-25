package k8s

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCronWorkload(t *testing.T) {
	yes := true
	j := batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "backup", CreationTimestamp: metav1.Now()}, Spec: batchv1.CronJobSpec{Suspend: &yes, JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Image: "busybox"}}}}}}}, Status: batchv1.CronJobStatus{Active: []corev1.ObjectReference{{Name: "backup-1"}}}}
	w := cronWorkload(j)
	if w.Kind != "CronJob" || w.Namespace != "apps" || w.Ready != 1 || w.Desired != 0 || !w.Available || w.Images != "busybox" {
		t.Errorf("cron workload: %+v", w)
	}
}

func TestNamespaceOf(t *testing.T) {
	n := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "metallb-system", Labels: map[string]string{"pod-security.kubernetes.io/enforce": "privileged"}}, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}}
	if got := namespaceOf(n); got.Name != "metallb-system" || got.Phase != "Active" || got.Security != "privileged" {
		t.Errorf("namespace: %+v", got)
	}
}
