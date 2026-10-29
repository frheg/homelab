// Package profile starts and stops groups of workloads. A profile is the set of
// namespaces labelled profile.kratos/<name>=true.
package profile

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	profilePrefix = "profile.kratos/"
	savedReplicas = "profile.kratos/replicas" // annotation: replicas before down
	consumerLabel = "gpu.kratos/consumer"     // owned by gpuctl, never touched here
	proxyLabel    = "gpu.kratos/proxy"        // wakes a consumer on request, must stay up
)

// leftAlone says why profctl must not scale a workload, or "" if it may.
func leftAlone(labels map[string]string) string {
	switch {
	case labels[consumerLabel] != "":
		return "gpu consumer, managed by gpuctl"
	case labels[proxyLabel] != "":
		return "gpu proxy, always on"
	}
	return ""
}

// workload is a Deployment or StatefulSet, reduced to what we need.
type workload struct {
	ns, kind, name string
	replicas       int32
	ready          int32
	annotations    map[string]string
	leftAlone      string
}

func namespaces(ctx context.Context, cs kubernetes.Interface, profile string) ([]string, error) {
	l, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: profilePrefix + profile + "=true"})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range l.Items {
		out = append(out, n.Name)
	}
	sort.Strings(out)
	return out, nil
}

// profiles lists every profile name found on namespaces, with its namespaces.
func Profiles(ctx context.Context, cs kubernetes.Interface) (map[string][]string, error) {
	l, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, n := range l.Items {
		for k, v := range n.Labels {
			if len(k) > len(profilePrefix) && k[:len(profilePrefix)] == profilePrefix && v == "true" {
				p := k[len(profilePrefix):]
				out[p] = append(out[p], n.Name)
			}
		}
	}
	for p := range out {
		sort.Strings(out[p])
	}
	return out, nil
}

func workloads(ctx context.Context, cs kubernetes.Interface, ns string) ([]workload, error) {
	var out []workload
	ds, err := cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, d := range ds.Items {
		out = append(out, workload{ns, "deployment", d.Name, replicasOf(d.Spec.Replicas), d.Status.ReadyReplicas, d.Annotations, leftAlone(d.Labels)})
	}
	ss, err := cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, s := range ss.Items {
		out = append(out, workload{ns, "statefulset", s.Name, replicasOf(s.Spec.Replicas), s.Status.ReadyReplicas, s.Annotations, leftAlone(s.Labels)})
	}
	return out, nil
}

func replicasOf(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

// scale sets replicas, and records or clears the saved count in one patch.
func scale(ctx context.Context, cs kubernetes.Interface, w workload, replicas int32, saved string) error {
	ann := "null"
	if saved != "" {
		ann = strconv.Quote(saved)
	}
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%q:%s}},"spec":{"replicas":%d}}`, savedReplicas, ann, replicas))
	var err error
	if w.kind == "deployment" {
		_, err = cs.AppsV1().Deployments(w.ns).Patch(ctx, w.name, types.MergePatchType, patch, metav1.PatchOptions{})
	} else {
		_, err = cs.AppsV1().StatefulSets(w.ns).Patch(ctx, w.name, types.MergePatchType, patch, metav1.PatchOptions{})
	}
	return err
}

// Down stops every workload not left alone in the profile and remembers its replicas.
func Down(ctx context.Context, cs kubernetes.Interface, profile string, out io.Writer) error {
	return each(ctx, cs, profile, out, func(w workload) (bool, error) {
		if w.replicas == 0 {
			return false, nil
		}
		fmt.Fprintf(out, "stop  %s %s/%s (was %d)\n", w.kind, w.ns, w.name, w.replicas)
		return true, scale(ctx, cs, w, 0, strconv.Itoa(int(w.replicas)))
	})
}

// Up starts every stopped workload not left alone at its saved replicas, default 1.
func Up(ctx context.Context, cs kubernetes.Interface, profile string, out io.Writer) error {
	return each(ctx, cs, profile, out, func(w workload) (bool, error) {
		if w.replicas > 0 {
			return false, nil
		}
		n := int32(1)
		if v, err := strconv.Atoi(w.annotations[savedReplicas]); err == nil && v > 0 {
			n = int32(v)
		}
		fmt.Fprintf(out, "start %s %s/%s (to %d)\n", w.kind, w.ns, w.name, n)
		return true, scale(ctx, cs, w, n, "")
	})
}

func each(ctx context.Context, cs kubernetes.Interface, profile string, out io.Writer, f func(workload) (bool, error)) error {
	nss, err := namespaces(ctx, cs, profile)
	if err != nil {
		return err
	}
	if len(nss) == 0 {
		return fmt.Errorf("no namespace has the label %s%s=true", profilePrefix, profile)
	}
	changed := false
	for _, ns := range nss {
		ws, err := workloads(ctx, cs, ns)
		if err != nil {
			return err
		}
		for _, w := range ws {
			if w.leftAlone != "" {
				continue
			}
			did, err := f(w)
			if err != nil {
				return fmt.Errorf("%s %s/%s: %w", w.kind, w.ns, w.name, err)
			}
			changed = changed || did
		}
	}
	if !changed {
		fmt.Fprintln(out, "nothing to do")
	}
	return nil
}

// Item is one workload of a profile, for UIs. Note says why it is left alone, if so.
type Item struct {
	Namespace, Kind, Name string
	Replicas, Ready       int32
	Note                  string
}

// Items lists every workload of the profile.
func Items(ctx context.Context, cs kubernetes.Interface, profile string) ([]Item, error) {
	nss, err := namespaces(ctx, cs, profile)
	if err != nil {
		return nil, err
	}
	if len(nss) == 0 {
		return nil, fmt.Errorf("no namespace has the label %s%s=true", profilePrefix, profile)
	}
	var out []Item
	for _, ns := range nss {
		ws, err := workloads(ctx, cs, ns)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			out = append(out, Item{w.ns, w.kind, w.name, w.replicas, w.ready, w.leftAlone})
		}
	}
	return out, nil
}

// Status prints one line per workload, GPU consumers marked.
func Status(ctx context.Context, cs kubernetes.Interface, profile string, out io.Writer) error {
	items, err := Items(ctx, cs, profile)
	if err != nil {
		return err
	}
	for _, it := range items {
		note := ""
		if it.Note != "" {
			note = "  (" + it.Note + ")"
		}
		fmt.Fprintf(out, "%-14s %s/%-24s %d/%d%s\n", it.Kind, it.Namespace, it.Name, it.Ready, it.Replicas, note)
	}
	return nil
}
