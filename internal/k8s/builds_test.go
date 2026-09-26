package k8s

import (
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildOf(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC))
	j := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "ana-phoenix"},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Command: []string{"buildctl", "build", "--output", "type=image,name=registry.kubit-builds.svc:5000/ana-phoenix:0.1.0,push=true"},
		}}}}},
		Status: batchv1.JobStatus{StartTime: &start, Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "old", Labels: map[string]string{"job-name": "ana-phoenix"}, CreationTimestamp: start}},
		{ObjectMeta: metav1.ObjectMeta{Name: "new", Labels: map[string]string{"job-name": "ana-phoenix"}, CreationTimestamp: metav1.NewTime(start.Add(time.Minute))}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other", Labels: map[string]string{"job-name": "cara-queue"}, CreationTimestamp: metav1.NewTime(start.Add(time.Hour))}},
	}
	b := buildOf(j, pods)
	if b.State != "succeeded" || b.Image != "registry.kubit-builds.svc:5000/ana-phoenix:0.1.0" || b.Pod != "new" || b.StartedAt != "2026-09-26T10:00:00Z" {
		t.Errorf("build = %+v", b)
	}
}
