package token

import (
	"context"
	"encoding/base64"
	"fmt"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Get returns the named WorkerJoinToken from the tenant cluster.
func Get(ctx context.Context, opts ...Option) (*clusteragentv1alpha1.WorkerJoinToken, error) {
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

	var tok clusteragentv1alpha1.WorkerJoinToken
	if err := c.Get(ctx, client.ObjectKey{Name: pCtx.name}, &tok); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("worker join token %q not found", pCtx.name)
		}
		return nil, fmt.Errorf("failed to get worker join token %q: %w", pCtx.name, err)
	}
	return &tok, nil
}

// FetchSecret retrieves the bootstrap kubeconfig a Ready WorkerJoinToken published
func FetchSecret(ctx context.Context, opts ...Option) ([]byte, error) {
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

	secretName := pCtx.name + "-jointoken"
	var secret corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: metav1.NamespaceSystem}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"worker join token secret for %q was not found, this might be because:\n"+
					"  (1) the WorkerJoinToken is not Ready yet, check with 'heir get workerJoinToken %s'\n"+
					"  (2) you're targeting the wrong tenant cluster (use --kubeconfig to point at the right one)",
				pCtx.name, pCtx.name,
			)
		}
		return nil, fmt.Errorf("failed to get worker join token secret %s/%s: %w", metav1.NamespaceSystem, secretName, err)
	}

	const secretKey = "jointoken"
	data, ok := secret.Data[secretKey]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no %q key", metav1.NamespaceSystem, secretName, secretKey)
	}
	return []byte(base64.StdEncoding.EncodeToString(data)), nil
}
