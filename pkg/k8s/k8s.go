package k8s

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func BuildClientConfig(kubeconfig, context string) clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	return cfg
}
func BuildClient(kubeconfig, contextName string) (*kubernetes.Clientset, error) {
	cfg := BuildClientConfig(kubeconfig, contextName)
	restConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}
	return client, nil
}

// BuildClientFromBytes builds a Kubernetes clientset from raw kubeconfig bytes rather
// than a file path. If overrideHost is non-empty, it replaces the server URL embedded
// in the kubeconfig before the client is built
func BuildClientFromBytes(raw []byte, overrideHost string) (*kubernetes.Clientset, []byte, string, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to build rest config from kubeconfig bytes: %w", err)
	}
	if overrideHost != "" {
		restConfig.Host = overrideHost
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to build kubernetes client: %w", err)
	}
	return client, restConfig.CAData, restConfig.Host, nil
}
