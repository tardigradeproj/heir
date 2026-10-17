package controlplane

import (
	"context"
	"fmt"

	heirruntime "github.com/tardigradeproj/heir/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FetchKubeconfig retrieves the admin kubeconfig Heir published for the named Runtime's tenant
// cluster, the "<name>-kubeconfig" Secret the controller maintains alongside it, optionally
// rewriting the server URL baked into it (e.g. for a local dev cluster reachable at
// 127.0.0.1 under a different address than the one its control plane advertises internally).
func FetchKubeconfig(ctx context.Context, opts ...Option) ([]byte, error) {
	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}

	c := pCtx.ctrlClient
	if c == nil {
		built, err := buildControllerClient(pCtx.kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build client: %w", err)
		}
		c = built
	}

	secretName := fmt.Sprintf("%s-kubeconfig", pCtx.name)
	var secret corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: pCtx.namespace}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"failed to generate cluster kubeconfig: cluster %q was not found, this might be because:\n"+
					"  (1) cluster provisioning is not fully complete yet — wait a bit and try again\n"+
					"  (2) your control plane was created in a different namespace (use --namespace to switch)\n"+
					"  (3) you're targeting the wrong management cluster (use --kubeconfig to point at the right one)",
				pCtx.name,
			)
		}
		return nil, fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	secretKey := heirruntime.NewControlPlaneLayout().Auth.ClientKubeconfig.SecretKey
	data, ok := secret.Data[secretKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no %q key", pCtx.namespace, secretName, secretKey)
	}

	if pCtx.server == "" {
		return data, nil
	}
	return rewriteServer(data, pCtx.server)
}

func rewriteServer(kubeconfigYAML []byte, server string) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfigYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig: %w", err)
	}
	for _, cluster := range cfg.Clusters {
		cluster.Server = server
	}
	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode kubeconfig: %w", err)
	}
	return out, nil
}
