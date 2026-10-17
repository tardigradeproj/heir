package token

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh/spinner"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/k8s"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Provision mints a WorkerJoinToken on the tenant cluster, and optionally waits for it to
// become Ready (a bootstrap token minted and its jointoken Secret published).
func Provision(ctx context.Context, opts ...Option) error {
	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}
	fmt.Printf("minting worker join token %q (ttl %s)\n", pCtx.name, pCtx.ttl)

	c := pCtx.ctrlClient
	if c == nil {
		built, err := buildControllerClient(pCtx.kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		c = built
	}

	token := &clusteragentv1alpha1.WorkerJoinToken{
		TypeMeta: metav1.TypeMeta{
			APIVersion: clusteragentv1alpha1.GroupVersion.String(),
			Kind:       "WorkerJoinToken",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: pCtx.name,
		},
		Spec: clusteragentv1alpha1.WorkerJoinTokenSpec{
			TTL: metav1.Duration{Duration: pCtx.ttl},
		},
	}

	if err := c.Create(ctx, token); err != nil {
		return fmt.Errorf("failed to create worker join token %q: %w", pCtx.name, err)
	}
	fmt.Printf("worker join token %q created\n", pCtx.name)

	if !pCtx.wait {
		return nil
	}
	return waitReady(ctx, c, token, pCtx.timeout)
}

func buildControllerClient(kubeconfig string) (client.Client, error) {
	restConfig, err := k8s.BuildClientConfig(kubeconfig, "").ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config: %w", err)
	}
	scheme := k8sruntime.NewScheme()
	if err := clusteragentv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to register clusteragent scheme: %w", err)
	}
	return client.New(restConfig, client.Options{Scheme: scheme})
}

func waitReady(ctx context.Context, c client.Client, token *clusteragentv1alpha1.WorkerJoinToken, timeout time.Duration) error {
	err := spinner.New().
		Title(fmt.Sprintf("waiting for worker join token %q to become ready (timeout %s)", token.Name, timeout)).
		Context(ctx).
		Output(os.Stderr).
		ActionWithErr(func(ctx context.Context) error {
			return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
				var current clusteragentv1alpha1.WorkerJoinToken
				if err := c.Get(ctx, client.ObjectKeyFromObject(token), &current); err != nil {
					return false, err
				}
				return meta.IsStatusConditionTrue(current.Status.Conditions, "Ready"), nil
			})
		}).
		Run()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for worker join token %q to become ready: %w", token.Name, err)
		}
		return fmt.Errorf("failed waiting for worker join token %q to become ready: %w", token.Name, err)
	}
	fmt.Printf("worker join token %q is ready\n", token.Name)
	return nil
}
