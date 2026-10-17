package token

import (
	"context"
	"fmt"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Delete removes the named WorkerJoinToken from the tenant cluster. Like kubectl delete, it's
// not idempotent: deleting a token that doesn't exist is an error, not a no-op.
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

	tok := &clusteragentv1alpha1.WorkerJoinToken{
		ObjectMeta: metav1.ObjectMeta{Name: pCtx.name},
	}
	if err := c.Delete(ctx, tok); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("worker join token %q not found", pCtx.name)
		}
		return fmt.Errorf("failed to delete worker join token %q: %w", pCtx.name, err)
	}
	fmt.Printf("worker join token %q deleted\n", pCtx.name)
	return nil
}
