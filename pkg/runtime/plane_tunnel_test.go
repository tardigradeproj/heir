package runtime

import (
	"testing"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGeneratePlaneTunnelServiceNodePort(t *testing.T) {
	wrkCtx := typ.NewWorkerContextWithDefaults()

	tests := []struct {
		name     string
		nodePort *int32
		want     int32
	}{
		{"nil NodePort produces 0 (not the CRD's own default)", nil, 0},
		{"explicit NodePort is propagated", ptr.To(int32(30081)), 30081},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &controlplanev1alpha1.Runtime{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "default"},
				Spec: controlplanev1alpha1.RuntimeSpec{
					ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
						PlaneTunnel: controlplanev1alpha1.PlaneTunnelSpec{
							Service: controlplanev1alpha1.PlaneTunnelServiceSpec{
								ServiceType: "ClusterIP",
								NodePort:    tt.nodePort,
							},
						},
					},
				},
			}

			services, err := GeneratePlaneTunnelService(*wrkCtx, runtime)
			require.NoError(t, err)
			require.Len(t, services, 3)

			tunnel := services[1]
			require.Equal(t, PlaneTunnelName(runtime.Name), tunnel.Name)
			require.Len(t, tunnel.Spec.Ports, 1)
			assert.Equal(t, tt.want, tunnel.Spec.Ports[0].NodePort)
		})
	}
}
