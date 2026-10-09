package controlplane

import "k8s.io/client-go/kubernetes"

type Option func(*provisionContext)

type provisionContext struct {
	name       string
	kubeconfig string
	namespace  string
	client     kubernetes.Interface // if set, skips buildClient (used in tests)
}

func WithName(name string) Option {
	return func(p *provisionContext) {
		p.name = name
	}
}

func WithKubeconfig(kubeconfig string) Option {
	return func(p *provisionContext) {
		p.kubeconfig = kubeconfig
	}
}

func WithNamespace(namespace string) Option {
	return func(p *provisionContext) {
		p.namespace = namespace
	}
}
