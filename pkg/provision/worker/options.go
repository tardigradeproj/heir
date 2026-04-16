package worker

type Option func(*joinContext)

type joinContext struct {
	token              string
	nodeLabels         map[string]string
	kubeletExtraArgs   map[string]string
	kubeProxyExtraArgs map[string]string
}

func WithKubeletExtraArgs(t map[string]string) Option {
	return func(j *joinContext) {
		j.kubeletExtraArgs = t
	}
}

func WithKubeProxyExtraArgs(t map[string]string) Option {
	return func(j *joinContext) {
		j.kubeProxyExtraArgs = t
	}
}

func WithNodeLabels(t map[string]string) Option {
	return func(j *joinContext) {
		j.nodeLabels = t
	}
}
