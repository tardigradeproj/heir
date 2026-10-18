package token

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// withControllerClient is a test-only Option that injects a pre-built controller-runtime
// client, bypassing buildControllerClient.
func withControllerClient(c client.Client) Option {
	return func(p *provisionContext) {
		p.ctrlClient = c
	}
}

func newTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	scheme := k8sruntime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, clusteragentv1alpha1.AddToScheme(scheme))
	return scheme
}

func TestProvision(t *testing.T) {
	const name = "new-node"
	scheme := newTestScheme(t)

	tests := []struct {
		name         string
		seedExisting bool
		wait         bool
		timeout      time.Duration
		wantErr      string // non-empty: err.Error() must contain this substring
		wantTimeout  bool   // true: just assert an error occurred, without matching its text
		validate     func(t *testing.T, c client.Client)
	}{
		{
			name: "creates a worker join token with the given ttl",
			wait: false,
			validate: func(t *testing.T, c client.Client) {
				var got clusteragentv1alpha1.WorkerJoinToken
				require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, &got))
				assert.Equal(t, 3*time.Hour, got.Spec.TTL.Duration)
			},
		},
		{
			name:         "create fails when the token already exists",
			seedExisting: true,
			wait:         false,
			wantErr:      "failed to create worker join token",
		},
		{
			name:        "wait times out when Ready never becomes true",
			wait:        true,
			timeout:     100 * time.Millisecond,
			wantTimeout: true,
			validate: func(t *testing.T, c client.Client) {
				// Create should have succeeded even though waiting for Ready timed out.
				var got clusteragentv1alpha1.WorkerJoinToken
				require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, &got))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.seedExisting {
				builder = builder.WithObjects(&clusteragentv1alpha1.WorkerJoinToken{
					ObjectMeta: metav1.ObjectMeta{Name: name},
				})
			}
			fakeClient := builder.Build()

			err := Provision(context.Background(),
				WithName(name),
				WithTTL(3*time.Hour),
				WithWait(tt.wait),
				WithTimeout(tt.timeout),
				withControllerClient(fakeClient),
			)

			switch {
			case tt.wantTimeout:
				assert.Error(t, err)
			case tt.wantErr != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			default:
				require.NoError(t, err)
			}

			if tt.validate != nil {
				tt.validate(t, fakeClient)
			}
		})
	}
}

func TestWaitReady(t *testing.T) {
	scheme := newTestScheme(t)

	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantErr    bool
	}{
		{
			name: "returns immediately when Ready is already true",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "TokenIssued", LastTransitionTime: metav1.Now()},
			},
			wantErr: false,
		},
		{
			name: "times out when Ready is false",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse, Reason: "TokenIssuanceFailed", LastTransitionTime: metav1.Now()},
			},
			wantErr: true,
		},
		{
			name:       "times out when there are no conditions at all",
			conditions: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &clusteragentv1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "new-node"},
				Status:     clusteragentv1alpha1.WorkerJoinTokenStatus{Conditions: tt.conditions},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(token).Build()

			err := waitReady(context.Background(), fakeClient, token, 100*time.Millisecond)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
