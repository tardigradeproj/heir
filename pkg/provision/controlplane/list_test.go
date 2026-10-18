package controlplane

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestList(t *testing.T) {
	const ns = "default"
	scheme := newTestScheme(t)

	t.Run("returns no items when none exist", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		got, err := List(context.Background(), WithNamespace(ns), withControllerClient(fakeClient))
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("returns every runtime in the namespace, sorted by name", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: "zebra", Namespace: ns}},
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: ns}},
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: "other-ns", Namespace: "other"}},
		).Build()

		got, err := List(context.Background(), WithNamespace(ns), withControllerClient(fakeClient))
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"alpha", "zebra"}, []string{got[0].Name, got[1].Name})
	})
}

func TestGet(t *testing.T) {
	const ns = "default"
	scheme := newTestScheme(t)

	t.Run("returns the named runtime", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&controlplanev1alpha1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: ns}},
		).Build()

		got, err := Get(context.Background(), WithName("my-cluster"), WithNamespace(ns), withControllerClient(fakeClient))
		require.NoError(t, err)
		assert.Equal(t, "my-cluster", got.Name)
	})

	t.Run("missing runtime produces a clear error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		_, err := Get(context.Background(), WithName("ghost"), WithNamespace(ns), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `runtime default/ghost not found`, err.Error())
	})
}
