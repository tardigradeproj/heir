package util

import (
	"errors"
	"fmt"

	"github.com/tardigradeproj/heir/pkg/pki"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Sentinel errors allow callers to branch on failure mode with errors.Is.
var (
	ErrInvalidKubeconfig = errors.New("kubeconfig is not valid")
	ErrMissingContext    = errors.New("kubeconfig is missing its current context")
	ErrMissingCluster    = errors.New("kubeconfig is missing the referenced cluster")
	ErrMissingAuthInfo   = errors.New("kubeconfig is missing the referenced auth info")
	ErrMissingClientCert = errors.New("kubeconfig auth info has no client certificate data")
	ErrServerMismatch    = errors.New("kubeconfig server does not match expectation")
	ErrInvalidClientCert = errors.New("kubeconfig client certificate is invalid")
)

// Kubeconfig bundles a parsed kubeconfig with the cluster and auth info resolved from
// its current context, so VerifyOptions don't need to re-resolve them.
type Kubeconfig struct {
	Config   *clientcmdapi.Config
	Cluster  *clientcmdapi.Cluster
	AuthInfo *clientcmdapi.AuthInfo
}

// VerifyOption is a self-contained kubeconfig check. It receives the resolved
// kubeconfig and returns an error describing why validation failed, or nil if the
// check passed.
type VerifyOption func(kc *Kubeconfig) error

// WithServerValidation checks that the kubeconfig's current cluster targets want.
func WithServerValidation(want string) VerifyOption {
	return func(kc *Kubeconfig) error {
		if kc.Cluster.Server != want {
			return fmt.Errorf("%w: got %q, want %q", ErrServerMismatch, kc.Cluster.Server, want)
		}
		return nil
	}
}

// WithClientCertificateValidation checks the kubeconfig's client certificate against
// ca and the given pki.VerifyOption checks, plus that it matches the kubeconfig's own
// client key.
func WithClientCertificateValidation(ca pki.Certificate, opts ...pki.VerifyOption) VerifyOption {
	return func(kc *Kubeconfig) error {
		checks := append([]pki.VerifyOption{pki.WithKeyValidation(kc.AuthInfo.ClientKeyData)}, opts...)
		if _, err := pki.ParseAndVerifyCertificate(kc.AuthInfo.ClientCertificateData, ca, checks...); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidClientCert, err)
		}
		return nil
	}
}

// ParseAndVerifyKubeconfig parses raw as a kubeconfig, resolves the cluster and auth
// info referenced by its current context, and runs each of the given VerifyOption
// checks against the result. Returns the resolved kubeconfig on success so callers can
// inspect further fields without re-parsing.
func ParseAndVerifyKubeconfig(raw []byte, opts ...VerifyOption) (*Kubeconfig, error) {
	config, err := clientcmd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKubeconfig, err)
	}

	kctx, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMissingContext, config.CurrentContext)
	}
	cluster, ok := config.Clusters[kctx.Cluster]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMissingCluster, kctx.Cluster)
	}
	authInfo, ok := config.AuthInfos[kctx.AuthInfo]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMissingAuthInfo, kctx.AuthInfo)
	}
	if len(authInfo.ClientCertificateData) == 0 {
		return nil, fmt.Errorf("%w: auth info %q", ErrMissingClientCert, kctx.AuthInfo)
	}

	kc := &Kubeconfig{
		Config:   config,
		Cluster:  cluster,
		AuthInfo: authInfo,
	}

	for _, opt := range opts {
		if err := opt(kc); err != nil {
			return nil, err
		}
	}

	return kc, nil
}
