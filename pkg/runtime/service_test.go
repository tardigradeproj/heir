package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func serviceRuntime(name, namespace string, svc controlplanev1alpha1.ServiceSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: controlplanev1alpha1.RuntimeSpec{
			ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
				Service: svc,
			},
		},
	}
}

func TestGenerateService(t *testing.T) {
	tests := []struct {
		name     string
		runtime  *controlplanev1alpha1.Runtime
		validate func(t *testing.T, svc *corev1.Service)
	}{
		{
			name: "name and namespace match the runtime",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				assert.Equal(t, "my-cluster", svc.Name)
				assert.Equal(t, "default", svc.Namespace)
			},
		},
		{
			name: "apiserver and konnectivity ports are always present",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				require.Len(t, svc.Spec.Ports, 2)
				p := svc.Spec.Ports[0]
				assert.Equal(t, "apiserver", p.Name)
				assert.Equal(t, int32(6443), p.Port)
				assert.Equal(t, intstr.FromInt32(6443), p.TargetPort)
				assert.Equal(t, corev1.ProtocolTCP, p.Protocol)
			},
		},
		{
			name: "service type is propagated from spec",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeNodePort,
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				assert.Equal(t, corev1.ServiceTypeNodePort, svc.Spec.Type)
			},
		},
		{
			name: "selector labels are set correctly",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				assert.Equal(t, "my-cluster", svc.Spec.Selector["app.kubernetes.io/name"])
				assert.Equal(t, "samaritano", svc.Spec.Selector["app.kubernetes.io/managed-by"])
			},
		},
		{
			name: "additional ports are appended after built-in ports",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeNodePort,
				AdditionalPorts: []controlplanev1alpha1.AdditionalPort{
					{
						Name:       "metrics",
						Port:       9090,
						TargetPort: intstr.FromInt32(9090),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				require.Len(t, svc.Spec.Ports, 3)
				assert.Equal(t, "apiserver", svc.Spec.Ports[0].Name)
				assert.Equal(t, "konnectivity", svc.Spec.Ports[1].Name)
				assert.Equal(t, "metrics", svc.Spec.Ports[2].Name)
				assert.Equal(t, int32(9090), svc.Spec.Ports[2].Port)
			},
		},
		{
			name: "additional metadata labels and annotations are set",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
				AdditionalMetadata: controlplanev1alpha1.AdditionalMetadata{
					Labels:      map[string]string{"env": "prod"},
					Annotations: map[string]string{"custom/annotation": "value"},
				},
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				assert.Equal(t, "prod", svc.Labels["env"])
				assert.Equal(t, "value", svc.Annotations["custom/annotation"])
			},
		},
		{
			name: "additional metadata labels do not override selector labels",
			runtime: serviceRuntime("my-cluster", "default", controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
				AdditionalMetadata: controlplanev1alpha1.AdditionalMetadata{
					Labels: map[string]string{"app.kubernetes.io/name": "overridden"},
				},
			}),
			validate: func(t *testing.T, svc *corev1.Service) {
				assert.Equal(t, "my-cluster", svc.Spec.Selector["app.kubernetes.io/name"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := GenerateService(tt.runtime, typ.NewWorkerContextWithDefaults())
			require.NoError(t, err)
			require.NotNil(t, svc)
			tt.validate(t, svc)
		})
	}
}
