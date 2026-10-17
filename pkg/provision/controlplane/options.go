package controlplane

import (
	"time"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Option func(*provisionContext)

type provisionContext struct {
	name       string
	kubeconfig string
	namespace  string
	set        []string
	wait       bool
	timeout    time.Duration
	server     string
	client     kubernetes.Interface // if set, skips buildClient (used in tests)
	ctrlClient client.Client        // if set, skips buildControllerClient (used in tests)
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

func WithSet(set []string) Option {
	return func(p *provisionContext) {
		p.set = set
	}
}

func WithWait(wait bool) Option {
	return func(p *provisionContext) {
		p.wait = wait
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(p *provisionContext) {
		p.timeout = timeout
	}
}

// WithServer overrides the server URL baked into a fetched kubeconfig. Empty leaves it
// untouched.
func WithServer(server string) Option {
	return func(p *provisionContext) {
		p.server = server
	}
}
