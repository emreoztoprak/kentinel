// Package k8s wraps client-go with the small set of operations the UI and
// agent need: resource listing/reading/updating, logs, exec, events, and a
// cluster overview. All access goes through the dynamic client with a static
// kind registry so there is a single code path for every resource kind.
package k8s

import (
	"fmt"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client bundles the typed and dynamic Kubernetes clients.
type Client struct {
	Clientset  kubernetes.Interface
	Dynamic    dynamic.Interface
	RestConfig *rest.Config
}

// NewClient connects to the cluster. Resolution order:
//  1. explicit kubeconfig path (KUBECONFIG_PATH)
//  2. in-cluster service account (when running inside a pod)
//  3. default kubeconfig loading rules (KUBECONFIG env, ~/.kube/config)
func NewClient(kubeconfigPath string) (*Client, error) {
	cfg, err := buildRestConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &Client{Clientset: clientset, Dynamic: dyn, RestConfig: cfg}, nil
}

func buildRestConfig(explicitPath string) (*rest.Config, error) {
	if explicitPath == "" {
		if _, inCluster := os.LookupEnv("KUBERNETES_SERVICE_HOST"); inCluster {
			return rest.InClusterConfig()
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if explicitPath != "" {
		rules.ExplicitPath = explicitPath
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
}
