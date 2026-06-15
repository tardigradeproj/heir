package runtime

import (
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GenerateService builds the control-plane Service for the given Runtime. No API calls are made;
// the caller is responsible for setting the owner reference and persisting the result.
func GenerateService(runtime *controlplanev1alpha1.Runtime, wrkCtx *typ.WorkerContext) (*corev1.Service, error) {
	svcSpec := runtime.Spec.ControlPlane.Service
	selectorLabels := map[string]string{
		"app.kubernetes.io/name":       runtime.Name,
		"app.kubernetes.io/managed-by": "heir",
	}

	ports := []corev1.ServicePort{
		{
			Name:       "apiserver",
			Port:       6443,
			TargetPort: intstr.FromInt32(6443),
			Protocol:   corev1.ProtocolTCP,
			NodePort:   svcSpec.ApiServerNodePort,
		},
		{
			Name:       "konnectivity",
			Port:       wrkCtx.KonnectivityProxyServerPort,
			TargetPort: intstr.FromInt32(wrkCtx.KonnectivityProxyServerPort),
			Protocol:   corev1.ProtocolTCP,
			NodePort:   svcSpec.KonnectivityNodePort,
		},
	}
	for _, p := range svcSpec.AdditionalPorts {
		ports = append(ports, corev1.ServicePort{
			Name:        p.Name,
			Port:        p.Port,
			TargetPort:  p.TargetPort,
			Protocol:    p.Protocol,
			AppProtocol: p.AppProtocol,
		})
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtime.Name,
			Namespace: runtime.Namespace,
			Labels:    MergeArgs(selectorLabels, svcSpec.AdditionalMetadata.Labels),
			Annotations: MergeArgs(
				map[string]string{"controlplane.tardigrade.runtime.io/deletion-protection": "false"},
				svcSpec.AdditionalMetadata.Annotations,
			),
		},
		Spec: corev1.ServiceSpec{
			Type:     svcSpec.ServiceType,
			Selector: selectorLabels,
			Ports:    ports,
		},
	}, nil
}
