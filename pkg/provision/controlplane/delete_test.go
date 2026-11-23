package controlplane

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDelete(t *testing.T) {
	const (
		name = "test-cluster"
		ns   = "default"
	)
	scheme := newTestScheme(t)

	t.Run("deletes the runtime", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		).Build()

		err := Delete(context.Background(), WithName(name), WithNamespace(ns), withControllerClient(fakeClient))
		require.NoError(t, err)

		var got controlplanev1alpha1.Runtime
		err = fakeClient.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, &got)
		assert.True(t, apierrors.IsNotFound(err), "expected runtime to be deleted, got err: %v", err)
	})

	t.Run("missing runtime produces a clear error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		err := Delete(context.Background(), WithName(name), WithNamespace(ns), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `runtime default/test-cluster not found`, err.Error())
	})

	t.Run("delete failure returns a wrapped error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return fmt.Errorf("injected: delete failed")
			},
		}).Build()

		err := Delete(context.Background(), WithName(name), WithNamespace(ns), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("failed to delete runtime %s/%s", ns, name))
	})
}
