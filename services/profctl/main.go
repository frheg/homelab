// profctl starts and stops groups of services. A profile is the set of
// namespaces labelled profile.kratos/<name>=true. GPU consumers and their proxies are left alone.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const usage = `usage: profctl <command> [profile]

  list              profiles and their namespaces
  status <profile>  every workload, ready/desired
  up <profile>      start what is stopped (saved replicas, default 1)
  down <profile>    stop everything except GPU consumers and proxies

exit codes: 0 ok, 1 error
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		fail(fmt.Errorf("kubeconfig: %w", err))
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	cmd := os.Args[1]
	if cmd == "list" {
		ps, err := profiles(ctx, cs)
		if err != nil {
			fail(err)
		}
		names := make([]string, 0, len(ps))
		for p := range ps {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, p := range names {
			fmt.Printf("%-12s %v\n", p, ps[p])
		}
		return
	}
	if len(os.Args) < 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	p := os.Args[2]
	switch cmd {
	case "status":
		err = status(ctx, cs, p, os.Stdout)
	case "up":
		err = up(ctx, cs, p, os.Stdout)
	case "down":
		err = down(ctx, cs, p, os.Stdout)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
