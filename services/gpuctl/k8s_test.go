package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func pod(p corev1.PodPhase) corev1.Pod { return corev1.Pod{Status: corev1.PodStatus{Phase: p}} }

func TestAnyHoldsGPU(t *testing.T) {
	cases := []struct {
		name string
		pods []corev1.Pod
		want bool
	}{
		{"none", nil, false},
		{"failed only", []corev1.Pod{pod(corev1.PodFailed)}, false},
		{"succeeded only", []corev1.Pod{pod(corev1.PodSucceeded)}, false},
		{"running", []corev1.Pod{pod(corev1.PodRunning)}, true},
		{"pending", []corev1.Pod{pod(corev1.PodPending)}, true},
		{"failed and running", []corev1.Pod{pod(corev1.PodFailed), pod(corev1.PodRunning)}, true},
	}
	for _, c := range cases {
		if got := anyHoldsGPU(c.pods); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
