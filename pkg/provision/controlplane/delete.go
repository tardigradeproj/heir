package controlplane

import (
	"context"
	"fmt"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Delete removes the named Runtime from the management cluster. The controller sets an
// owner reference from every Deployment, Service, ConfigMap and Secret it creates back to
// the Runtime, so deleting the Runtime lets Kubernetes garbage collection remove all of
// them; nothing else needs to be deleted directly.
func Delete(ctx context.Context, opts ...Option) error {
	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}

	c := pCtx.ctrlClient
	if c == nil {
		built, err := buildControllerClient(pCtx.kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		c = built
	}

	key := client.ObjectKey{Name: pCtx.name, Namespace: pCtx.namespace}
	var rt controlplanev1alpha1.Runtime
	if err := c.Get(ctx, key, &rt); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("runtime %s/%s not found", pCtx.namespace, pCtx.name)
		}
		return fmt.Errorf("failed to get runtime %s/%s: %w", pCtx.namespace, pCtx.name, err)
	}

	if err := c.Delete(ctx, &rt); err != nil {
		return fmt.Errorf("failed to delete runtime %s/%s: %w", pCtx.namespace, pCtx.name, err)
	}
	return nil
}
