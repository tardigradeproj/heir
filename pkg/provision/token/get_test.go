package token

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGet(t *testing.T) {
	scheme := newTestScheme(t)

	t.Run("returns the named token", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&clusteragentv1alpha1.WorkerJoinToken{ObjectMeta: metav1.ObjectMeta{Name: "new-node"}},
		).Build()

		got, err := Get(context.Background(), WithName("new-node"), withControllerClient(fakeClient))
		require.NoError(t, err)
		assert.Equal(t, "new-node", got.Name)
	})

	t.Run("missing token produces a clear error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		_, err := Get(context.Background(), WithName("ghost"), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `worker join token "ghost" not found`, err.Error())
	})
}

func TestFetchSecret(t *testing.T) {
	scheme := newTestScheme(t)

	t.Run("returns the jointoken value base64-encoded, not decoded", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "new-node-jointoken", Namespace: metav1.NamespaceSystem},
				// client-go's Secret.Data is already decoded from the wire format, so this is
				// the raw kubeconfig bytes a real controller would store here.
				Data: map[string][]byte{"jointoken": []byte("fake-bootstrap-kubeconfig")},
			},
		).Build()

		got, err := FetchSecret(context.Background(), WithName("new-node"), withControllerClient(fakeClient))
		require.NoError(t, err)
		// heir provision worker --token base64-decodes its input itself, so FetchSecret must
		// hand back the encoded form, not the raw bytes above.
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("fake-bootstrap-kubeconfig")), string(got))
	})

	t.Run("missing secret produces a clear, actionable error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		_, err := FetchSecret(context.Background(), WithName("new-node"), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `worker join token secret for "new-node" was not found, this might be because:
  (1) the WorkerJoinToken is not Ready yet, check with 'heir get workerJoinToken new-node'
  (2) you're targeting the wrong tenant cluster (use --kubeconfig to point at the right one)`, err.Error())
	})

	t.Run("secret without a jointoken key produces a clear error", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "new-node-jointoken", Namespace: metav1.NamespaceSystem},
				Data:       map[string][]byte{"not-jointoken": []byte("irrelevant")},
			},
		).Build()

		_, err := FetchSecret(context.Background(), WithName("new-node"), withControllerClient(fakeClient))
		require.Error(t, err)
		assert.Equal(t, `secret kube-system/new-node-jointoken has no "jointoken" key`, err.Error())
	})
}
