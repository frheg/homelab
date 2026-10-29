// panel is the web page with the buttons: GPU reserve and switch, profile up and down.
// It keeps no state. gpuctl and the cluster are the truth, the page polls them.
package main

import (
	"flag"
	"log"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	gpuctl := flag.String("gpuctl", "http://gpuctl.gpu-system", "gpuctl address")
	flag.Parse()

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		log.Fatalf("kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("panel on %s, gpuctl %s", *addr, *gpuctl)
	log.Fatal(http.ListenAndServe(*addr, New(cs, *gpuctl).Handler()))
}
