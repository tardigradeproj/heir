package token

import (
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Option func(*provisionContext)

type provisionContext struct {
	name       string
	ttl        time.Duration
	kubeconfig string
	wait       bool
	timeout    time.Duration
	ctrlClient client.Client // if set, skips buildControllerClient (used in tests)
}

func WithName(name string) Option {
	return func(p *provisionContext) {
		p.name = name
	}
}

func WithTTL(ttl time.Duration) Option {
	return func(p *provisionContext) {
		p.ttl = ttl
	}
}

// WithKubeconfig sets the path to the tenant cluster's kubeconfig — not the management
// cluster's. WorkerJoinToken is reconciled by clusteragent directly against the tenant
// cluster, so that's what every command in this package targets.
func WithKubeconfig(kubeconfig string) Option {
	return func(p *provisionContext) {
		p.kubeconfig = kubeconfig
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
