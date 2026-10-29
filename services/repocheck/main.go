// repocheck reads rendered Kubernetes manifests on stdin and enforces the
// rules in docs/conventions.md that generic linters do not know.
// Usage: kubectl kustomize platform | repocheck
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	consumerLabel = "gpu.kratos/consumer"
	pruneAnnot    = "kustomize.toolkit.fluxcd.io/prune"
)

type meta struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

type container struct {
	Name      string `yaml:"name"`
	Image     string `yaml:"image"`
	Resources struct {
		Requests map[string]any `yaml:"requests"`
		Limits   map[string]any `yaml:"limits"`
	} `yaml:"resources"`
}

type podTemplate struct {
	Metadata meta `yaml:"metadata"`
	Spec     struct {
		Containers     []container `yaml:"containers"`
		InitContainers []container `yaml:"initContainers"`
	} `yaml:"spec"`
}

type doc struct {
	Kind       string            `yaml:"kind"`
	Metadata   meta              `yaml:"metadata"`
	Type       string            `yaml:"type"`
	Data       map[string]string `yaml:"data"`
	StringData map[string]string `yaml:"stringData"`
	Spec       struct {
		Replicas *int `yaml:"replicas"`
		Strategy struct {
			Type string `yaml:"type"`
		} `yaml:"strategy"`
		Template    podTemplate `yaml:"template"`
		JobTemplate struct {
			Spec struct {
				Template podTemplate `yaml:"template"`
			} `yaml:"spec"`
		} `yaml:"jobTemplate"`
		Chart struct {
			Spec struct {
				Version string `yaml:"version"`
			} `yaml:"spec"`
		} `yaml:"chart"`
	} `yaml:"spec"`
}

func (d doc) id() string {
	if d.Metadata.Namespace != "" {
		return fmt.Sprintf("%s %s/%s", d.Kind, d.Metadata.Namespace, d.Metadata.Name)
	}
	return fmt.Sprintf("%s %s", d.Kind, d.Metadata.Name)
}

// check returns one line per violation.
func check(d doc) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, d.id()+": "+fmt.Sprintf(format, a...)) }

	switch d.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		tpl := d.Spec.Template
		if d.Kind == "CronJob" {
			tpl = d.Spec.JobTemplate.Spec.Template
		}
		for _, c := range append(append([]container{}, tpl.Spec.InitContainers...), tpl.Spec.Containers...) {
			if !strings.Contains(c.Image, "@sha256:") {
				add("container %s: image %q is not pinned by digest", c.Name, c.Image)
			}
			if c.Resources.Requests == nil || c.Resources.Limits == nil || c.Resources.Limits["memory"] == nil {
				add("container %s: needs resource requests and a memory limit", c.Name)
			}
		}
		if lbl, ok := d.Metadata.Labels[consumerLabel]; ok {
			if d.Spec.Replicas != nil {
				add("GPU consumer %q must not set replicas, gpuctl owns it", lbl)
			}
			if d.Spec.Strategy.Type != "Recreate" {
				add("GPU consumer %q needs strategy Recreate", lbl)
			}
			if tpl.Metadata.Labels[consumerLabel] != lbl {
				add("GPU consumer %q: pod template lacks the same %s label", lbl, consumerLabel)
			}
		}
	case "Secret":
		for _, m := range []map[string]string{d.Data, d.StringData} {
			for k, v := range m {
				if !strings.HasPrefix(v, "ENC[") {
					add("key %q is not SOPS-encrypted", k)
				}
			}
		}
	case "PersistentVolumeClaim":
		if d.Metadata.Annotations[pruneAnnot] != "disabled" {
			add("needs annotation %s: disabled, local-path deletes data with the claim", pruneAnnot)
		}
	case "HelmRelease":
		v := d.Spec.Chart.Spec.Version
		if v == "" || strings.ContainsAny(v, "*^~<>|") {
			add("chart version %q must be an exact version", v)
		}
	}
	return out
}

func run(r io.Reader) ([]string, error) {
	dec := yaml.NewDecoder(r)
	var all []string
	seen := 0
	for {
		var d doc
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			if seen == 0 {
				return all, errors.New("no manifests on stdin, refusing to pass")
			}
			return all, nil
		}
		if err != nil {
			return all, fmt.Errorf("parse manifests: %w", err)
		}
		if d.Kind != "" {
			seen++
			all = append(all, check(d)...)
		}
	}
}

func main() {
	bad, err := run(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, l := range bad {
		fmt.Println(l)
	}
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "%d violation(s)\n", len(bad))
		os.Exit(1)
	}
}
