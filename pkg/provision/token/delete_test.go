package token

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDelete(t *testing.T) {
	scheme := newTestScheme(t)

	t.Run("deletes the named token", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&clusteragentv1alpha1.WorkerJoinToken{ObjectMeta: metav1.ObjectMeta{Name: "new-node"}},
		).Build()

		err := Delete(context.Background(), WithName("new-node"), withControllerClient(fakeClient))
		require.NoError(t, err)

		var got clusteragentv1alpha1.WorkerJoinToken
		err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "new-node"}, &got)
		assert.True(t, apierrors.IsNotFound(err), "expected token to be gone, got err: %v", err)
	})

	t.Run("deleting a missing token is an error, not a no-op", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		err := Delete(context.Background(), WithName("ghost"), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `worker join token "ghost" not found`, err.Error())
	})
}
