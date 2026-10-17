package token

import (
	"context"
	"fmt"
	"sort"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
)

// List returns every WorkerJoinToken on the tenant cluster, sorted by name for deterministic
// output.
func List(ctx context.Context, opts ...Option) ([]clusteragentv1alpha1.WorkerJoinToken, error) {
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

	var list clusteragentv1alpha1.WorkerJoinTokenList
	if err := c.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("failed to list worker join tokens: %w", err)
	}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}
