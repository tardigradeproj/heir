package kube

// Builder represents and implementation of building Kubernetes
// building may constitute downloading a release
type Builder interface {
	// Build returns a Bits and any errors encountered while building Kubernetes.
	// Some implementations (upstream binaries) may use this step to obtain
	// an existing build instead
	Build() (Bits, error)
}
