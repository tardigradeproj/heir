package token

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestList(t *testing.T) {
	scheme := newTestScheme(t)

	t.Run("returns no items when none exist", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		got, err := List(context.Background(), withControllerClient(fakeClient))
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("returns every token sorted by name", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&clusteragentv1alpha1.WorkerJoinToken{ObjectMeta: metav1.ObjectMeta{Name: "zebra"}},
			&clusteragentv1alpha1.WorkerJoinToken{ObjectMeta: metav1.ObjectMeta{Name: "alpha"}},
			&clusteragentv1alpha1.WorkerJoinToken{ObjectMeta: metav1.ObjectMeta{Name: "mid"}},
		).Build()

		got, err := List(context.Background(), withControllerClient(fakeClient))
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []string{"alpha", "mid", "zebra"}, []string{got[0].Name, got[1].Name, got[2].Name})
	})
}
