package controlplane

import (
	"context"
	"fmt"
	"sort"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// List returns every Runtime in the given namespace, sorted by name for deterministic output.
func List(ctx context.Context, opts ...Option) ([]controlplanev1alpha1.Runtime, error) {
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

	var list controlplanev1alpha1.RuntimeList
	if err := c.List(ctx, &list, client.InNamespace(pCtx.namespace)); err != nil {
		return nil, fmt.Errorf("failed to list runtimes: %w", err)
	}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}

// Get returns the named Runtime from the management cluster.
func Get(ctx context.Context, opts ...Option) (*controlplanev1alpha1.Runtime, error) {
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

	var rt controlplanev1alpha1.Runtime
	if err := c.Get(ctx, client.ObjectKey{Name: pCtx.name, Namespace: pCtx.namespace}, &rt); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("runtime %s/%s not found", pCtx.namespace, pCtx.name)
		}
		return nil, fmt.Errorf("failed to get runtime %s/%s: %w", pCtx.namespace, pCtx.name, err)
	}
	return &rt, nil
}
