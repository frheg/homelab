package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Deployments and their pods carry this label. The value is the consumer name.
const consumerLabel = "gpu.kratos/consumer"

const pollEvery = time.Second

// kube implements Cluster and Store on a real cluster.
type kube struct {
	cs   kubernetes.Interface
	ns   string // namespace of the state ConfigMap
	name string // state ConfigMap name
}

func newKube(ns, name string) (*kube, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// outside a cluster, use the default kubeconfig
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("kubeconfig: %w", err)
		}
	}
	return newKubeFrom(cfg, ns, name)
}

func newKubeFrom(cfg *rest.Config, ns, name string) (*kube, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &kube{cs: cs, ns: ns, name: name}, nil
}

func (k *kube) deployments(ctx context.Context, consumer string) ([]appsv1.Deployment, error) {
	l, err := k.cs.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: consumerLabel + "=" + consumer,
	})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (k *kube) Consumers(ctx context.Context) ([]string, error) {
	l, err := k.cs.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: consumerLabel,
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, d := range l.Items {
		seen[d.Labels[consumerLabel]] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

func (k *kube) Scale(ctx context.Context, consumer string, replicas int32) error {
	ds, err := k.deployments(ctx, consumer)
	if err != nil {
		return err
	}
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	for _, d := range ds {
		_, err := k.cs.AppsV1().Deployments(d.Namespace).Patch(ctx, d.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("scale %s/%s: %w", d.Namespace, d.Name, err)
		}
	}
	return nil
}

func (k *kube) WaitGone(ctx context.Context, consumer string) error {
	return poll(ctx, func() (bool, error) {
		l, err := k.cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			LabelSelector: consumerLabel + "=" + consumer,
		})
		if err != nil {
			return false, err
		}
		return !anyHoldsGPU(l.Items), nil
	})
}

func (k *kube) WaitReady(ctx context.Context, consumer string) error {
	return poll(ctx, func() (bool, error) {
		ds, err := k.deployments(ctx, consumer)
		if err != nil {
			return false, err
		}
		if len(ds) == 0 {
			return false, fmt.Errorf("no deployment for %s", consumer)
		}
		for _, d := range ds {
			want := int32(1)
			if d.Spec.Replicas != nil {
				want = *d.Spec.Replicas
			}
			if d.Status.ObservedGeneration < d.Generation ||
				d.Status.ReadyReplicas < want || d.Status.Replicas != d.Status.ReadyReplicas {
				return false, nil
			}
		}
		return true, nil
	})
}

// poll runs f every pollEvery until it says done, errors, or ctx ends.
func poll(ctx context.Context, f func() (bool, error)) error {
	for {
		done, err := f()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-time.After(pollEvery):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (k *kube) Load(ctx context.Context) (State, error) {
	cm, err := k.cs.CoreV1().ConfigMaps(k.ns).Get(ctx, k.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if raw := cm.Data["state"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return State{}, fmt.Errorf("parse state: %w", err)
		}
	}
	return s, nil
}

func (k *kube) Save(ctx context.Context, s State) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	cms := k.cs.CoreV1().ConfigMaps(k.ns)
	cm, err := cms.Get(ctx, k.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cms.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: k.name, Namespace: k.ns},
			Data:       map[string]string{"state": string(raw)},
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	cm.Data = map[string]string{"state": string(raw)}
	_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// anyHoldsGPU is true if a pod may still hold the GPU. Finished pods
// (Succeeded or Failed) hold nothing, but their objects can stay around.
// A pod being deleted is still Running until its containers exit, so it counts.
func anyHoldsGPU(pods []corev1.Pod) bool {
	for _, p := range pods {
		if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			return true
		}
	}
	return false
}

// Replicas is the desired replica count summed over the consumer's Deployments.
func (k *kube) Replicas(ctx context.Context, consumer string) (int32, error) {
	ds, err := k.deployments(ctx, consumer)
	if err != nil {
		return 0, err
	}
	var n int32
	for _, d := range ds {
		if d.Spec.Replicas == nil {
			n++ // Kubernetes defaults to 1
		} else {
			n += *d.Spec.Replicas
		}
	}
	return n, nil
}

// startTimeoutAnnotation lets a slow consumer say how long it may take to become ready.
const startTimeoutAnnotation = "gpu.kratos/start-timeout"

// StartTimeout is the longest start-timeout annotation over the consumer's Deployments.
func (k *kube) StartTimeout(ctx context.Context, consumer string) (time.Duration, error) {
	ds, err := k.deployments(ctx, consumer)
	if err != nil {
		return 0, err
	}
	var longest time.Duration
	for _, d := range ds {
		v := d.Annotations[startTimeoutAnnotation]
		if v == "" {
			continue
		}
		t, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("%s/%s: bad %s %q: %w", d.Namespace, d.Name, startTimeoutAnnotation, v, err)
		}
		if t > longest {
			longest = t
		}
	}
	return longest, nil
}

// yieldLabel marks a Deployment that must not run while the named consumer
// holds the GPU, because it competes for RAM. Set it on the pod template too.
const yieldLabel = "gpu.kratos/yields-to"

// pausedAnnotation remembers the replicas of a Deployment that was paused.
const pausedAnnotation = "gpu.kratos/paused-replicas"

func (k *kube) yielders(ctx context.Context, consumer string) ([]appsv1.Deployment, error) {
	l, err := k.cs.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: yieldLabel + "=" + consumer,
	})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

// Pause scales the Deployments that yield to consumer to 0 and waits until
// their pods are gone, so the RAM is free. It remembers the replicas.
func (k *kube) Pause(ctx context.Context, consumer string) error {
	ds, err := k.yielders(ctx, consumer)
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		return nil
	}
	for _, d := range ds {
		n := int32(1)
		if d.Spec.Replicas != nil {
			n = *d.Spec.Replicas
		}
		if n == 0 {
			continue
		}
		patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:"%d"}},"spec":{"replicas":0}}`, pausedAnnotation, n))
		if _, err := k.cs.AppsV1().Deployments(d.Namespace).Patch(ctx, d.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("pause %s/%s: %w", d.Namespace, d.Name, err)
		}
	}
	return poll(ctx, func() (bool, error) {
		l, err := k.cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			LabelSelector: yieldLabel + "=" + consumer,
		})
		if err != nil {
			return false, err
		}
		return !anyHoldsGPU(l.Items), nil
	})
}

// Resume starts the Deployments that Pause stopped, at their saved replicas.
// One that someone started by hand in the meantime is left running.
func (k *kube) Resume(ctx context.Context, consumer string) error {
	ds, err := k.yielders(ctx, consumer)
	if err != nil {
		return err
	}
	for _, d := range ds {
		saved, ok := d.Annotations[pausedAnnotation]
		if !ok {
			continue
		}
		patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:null}}}`, pausedAnnotation)
		if n, err := strconv.Atoi(saved); err == nil && n > 0 && d.Spec.Replicas != nil && *d.Spec.Replicas == 0 {
			patch = fmt.Sprintf(`{"metadata":{"annotations":{%q:null}},"spec":{"replicas":%d}}`, pausedAnnotation, n)
		}
		if _, err := k.cs.AppsV1().Deployments(d.Namespace).Patch(ctx, d.Name, types.MergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("resume %s/%s: %w", d.Namespace, d.Name, err)
		}
	}
	return nil
}
