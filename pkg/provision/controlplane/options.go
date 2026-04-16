package controlplane

type Option func(*provisionContext)

type provisionContext struct {
	name              string
	config            string
	kubeconfig        string
	clusterKubeconfig string
	namespace         string
}

func WithName(name string) Option {
	return func(p *provisionContext) {
		p.name = name
	}
}

func WithConfig(config string) Option {
	return func(p *provisionContext) {
		p.config = config
	}
}

func WithKubeconfig(kubeconfig string) Option {
	return func(p *provisionContext) {
		p.kubeconfig = kubeconfig
	}
}

func WithClusterKubeconfig(clusterKubeconfig string) Option {
	return func(p *provisionContext) {
		p.clusterKubeconfig = clusterKubeconfig
	}
}

func WithNamespace(namespace string) Option {
	return func(p *provisionContext) {
		p.namespace = namespace
	}
}
