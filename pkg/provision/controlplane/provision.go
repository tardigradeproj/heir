package controlplane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/huh/spinner"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/strvals"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// Provision creates a Runtime on the management cluster, built from the CRD's own structural
// defaults overridden by any --set values, and optionally waits for it to become Available.
func Provision(ctx context.Context, opts ...Option) error {
	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}
	fmt.Printf("provisioning control plane %q (namespace %q)\n", pCtx.name, pCtx.namespace)

	c := pCtx.ctrlClient
	if c == nil {
		built, err := buildControllerClient(pCtx.kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build client: %w", err)
		}
		c = built
	}

	rt := &controlplanev1alpha1.Runtime{
		TypeMeta: metav1.TypeMeta{
			APIVersion: controlplanev1alpha1.GroupVersion.String(),
			Kind:       "Runtime",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pCtx.name,
			Namespace: pCtx.namespace,
		},
	}

	if err := applySetValues(rt, pCtx.set); err != nil {
		return fmt.Errorf("invalid --set value: %w", err)
	}

	if err := c.Create(ctx, rt); err != nil {
		return fmt.Errorf("failed to create runtime %s/%s: %w", pCtx.namespace, pCtx.name, err)
	}
	fmt.Printf("runtime %q created\n", pCtx.name)

	if !pCtx.wait {
		return nil
	}
	if err := waitAvailable(ctx, c, rt, pCtx.timeout); err != nil {
		return err
	}

	hint := fmt.Sprintf("heir provision cluster-kubeconfig --name %q --namespace %q", rt.Name, rt.Namespace)
	if pCtx.kubeconfig != "" {
		hint += fmt.Sprintf(" --kubeconfig %q", pCtx.kubeconfig)
	}
	fmt.Printf("cluster successfully provisioned. Run '%s' to obtain its kubeconfig\n", hint)
	return nil
}

func buildControllerClient(kubeconfig string) (client.Client, error) {
	restConfig, err := buildRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	scheme := k8sruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to register core scheme: %w", err)
	}
	if err := controlplanev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to register controlplane scheme: %w", err)
	}
	return client.New(restConfig, client.Options{Scheme: scheme})
}

func applySetValues(rt *controlplanev1alpha1.Runtime, assignments []string) error {
	if len(assignments) == 0 {
		return nil
	}

	raw, err := yaml.Marshal(rt.Spec)
	if err != nil {
		return fmt.Errorf("failed to marshal runtime spec: %w", err)
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("failed to decode runtime spec: %w", err)
	}

	for _, assignment := range assignments {
		if err := strvals.ParseInto(assignment, obj); err != nil {
			return fmt.Errorf("--set %q: %w", assignment, err)
		}
	}

	merged, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to encode merged runtime spec: %w", err)
	}
	return yaml.Unmarshal(merged, &rt.Spec)
}

func waitAvailable(ctx context.Context, c client.Client, rt *controlplanev1alpha1.Runtime, timeout time.Duration) error {
	err := spinner.New().
		Title(fmt.Sprintf("waiting for runtime %q to become available (timeout %s)", rt.Name, timeout)).
		Context(ctx).
		Output(os.Stderr).
		ActionWithErr(func(ctx context.Context) error {
			return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
				var current controlplanev1alpha1.Runtime
				if err := c.Get(ctx, client.ObjectKeyFromObject(rt), &current); err != nil {
					return false, err
				}
				return meta.IsStatusConditionTrue(current.Status.Conditions, "Available"), nil
			})
		}).
		Run()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for runtime %q to become available: %w", rt.Name, err)
		}
		return fmt.Errorf("failed waiting for runtime %q to become available: %w", rt.Name, err)
	}
	return nil
}
