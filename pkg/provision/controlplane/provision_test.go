package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	require.NoError(t, controlplanev1alpha1.AddToScheme(scheme))
	return scheme
}

func TestApplySetValues(t *testing.T) {
	tests := []struct {
		name        string
		assignments []string
		wantErr     string // non-empty: err.Error() must contain this substring
		validate    func(t *testing.T, rt *controlplanev1alpha1.Runtime)
	}{
		{
			name:        "no assignments leaves spec untouched",
			assignments: nil,
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, controlplanev1alpha1.RuntimeSpec{}, rt.Spec)
			},
		},
		{
			name:        "sets a simple nested scalar",
			assignments: []string{"controlPlane.planeTunnel.server.image=abc"},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, "abc", rt.Spec.ControlPlane.PlaneTunnel.Server.Image)
			},
		},
		{
			name:        "sets a nested integer",
			assignments: []string{"cluster.controlPlaneExternalEndpoint.apiServer.port=4000"},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, int32(4000), rt.Spec.Cluster.ControlPlaneExternalEndpoint.APIServer.Port)
			},
		},
		{
			name:        "sets a list element by index",
			assignments: []string{"cluster.apiServer.sans[0]=foo"},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, []string{"foo"}, rt.Spec.Cluster.APIServer.Sans)
			},
		},
		{
			name:        "comma-separated assignments in a single --set",
			assignments: []string{"cluster.apiServer.sans[0]=foo,cluster.apiServer.sans[1]=bar"},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, []string{"foo", "bar"}, rt.Spec.Cluster.APIServer.Sans)
			},
		},
		{
			name: "separate --set flags for sibling fields both apply",
			assignments: []string{
				"controlPlane.planeTunnel.server.image=abc",
				"cluster.apiServer.sans[0]=foo",
			},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, "abc", rt.Spec.ControlPlane.PlaneTunnel.Server.Image)
				assert.Equal(t, []string{"foo"}, rt.Spec.Cluster.APIServer.Sans)
			},
		},
		{
			name: "later --set overrides an earlier one on the same path",
			assignments: []string{
				"controlPlane.planeTunnel.server.image=first",
				"controlPlane.planeTunnel.server.image=second",
			},
			validate: func(t *testing.T, rt *controlplanev1alpha1.Runtime) {
				assert.Equal(t, "second", rt.Spec.ControlPlane.PlaneTunnel.Server.Image)
			},
		},
		{
			name:        "missing '=' is a parse error",
			assignments: []string{"cluster.apiServer.sans"},
			wantErr:     `--set "cluster.apiServer.sans"`,
		},
		{
			name: "indexing into a scalar as if it were an object errors",
			assignments: []string{
				"controlPlane.planeTunnel.server.image=abc",
				"controlPlane.planeTunnel.server.image.nested=x",
			},
			wantErr: `--set "controlPlane.planeTunnel.server.image.nested=x"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &controlplanev1alpha1.Runtime{}
			err := applySetValues(rt, tt.assignments)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			tt.validate(t, rt)
		})
	}
}

func TestWaitAvailable(t *testing.T) {
	scheme := newTestScheme(t)

	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantErr    bool
	}{
		{
			name: "returns immediately when Available is already true",
			conditions: []metav1.Condition{
				{Type: "Available", Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()},
			},
			wantErr: false,
		},
		{
			name: "times out when Available is false",
			conditions: []metav1.Condition{
				{Type: "Available", Status: metav1.ConditionFalse, Reason: "NotReady", LastTransitionTime: metav1.Now()},
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
			rt := &controlplanev1alpha1.Runtime{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "default"},
				Status:     controlplanev1alpha1.RuntimeStatus{Conditions: tt.conditions},
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rt).Build()

			err := waitAvailable(context.Background(), fakeClient, rt, 100*time.Millisecond)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestProvision(t *testing.T) {
	const (
		name = "my-cluster"
		ns   = "default"
	)
	scheme := newTestScheme(t)

	tests := []struct {
		name         string
		set          []string
		wait         bool
		timeout      time.Duration
		seedExisting bool
		wantErr      string // non-empty: err.Error() must contain this substring
		wantTimeout  bool   // true: just assert an error occurred, without matching its text
		validate     func(t *testing.T, c client.Client)
	}{
		{
			name: "creates runtime with no overrides",
			wait: false,
			validate: func(t *testing.T, c client.Client) {
				var got controlplanev1alpha1.Runtime
				require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, &got))
				assert.Equal(t, controlplanev1alpha1.RuntimeSpec{}, got.Spec)
			},
		},
		{
			name: "creates runtime with --set overrides",
			set:  []string{"controlPlane.planeTunnel.server.image=abc"},
			wait: false,
			validate: func(t *testing.T, c client.Client) {
				var got controlplanev1alpha1.Runtime
				require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, &got))
				assert.Equal(t, "abc", got.Spec.ControlPlane.PlaneTunnel.Server.Image)
			},
		},
		{
			name:    "invalid --set value fails before creating",
			set:     []string{"badinput"},
			wait:    false,
			wantErr: "invalid --set value",
			validate: func(t *testing.T, c client.Client) {
				var got controlplanev1alpha1.Runtime
				err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, &got)
				assert.True(t, apierrors.IsNotFound(err), "expected runtime not to have been created, got err: %v", err)
			},
		},
		{
			name:         "create fails when the runtime already exists",
			seedExisting: true,
			wait:         false,
			wantErr:      "failed to create runtime",
		},
		{
			name:        "wait times out when Available never becomes true",
			wait:        true,
			timeout:     100 * time.Millisecond,
			wantTimeout: true,
			validate: func(t *testing.T, c client.Client) {
				// Create should have succeeded even though waiting for Available timed out.
				var got controlplanev1alpha1.Runtime
				require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: ns}, &got))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.seedExisting {
				builder = builder.WithObjects(&controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
				})
			}
			fakeClient := builder.Build()

			opts := []Option{
				WithName(name),
				WithNamespace(ns),
				WithSet(tt.set),
				WithWait(tt.wait),
				WithTimeout(tt.timeout),
				withControllerClient(fakeClient),
			}

			err := Provision(context.Background(), opts...)

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
