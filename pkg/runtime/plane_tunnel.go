package runtime

import (
	"fmt"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func PlaneTunnelName(runtimeName string) string {
	return fmt.Sprintf("tunnel-server-%s", runtimeName)
}

func PlaneTunnelHeadlessName(runtimeName string) string {
	return fmt.Sprintf("tunnel-server-%s-headless", runtimeName)
}

func PlaneTunnelEgressName(runtimeName string) string {
	return fmt.Sprintf("tunnel-server-%s-egress", runtimeName)
}

func GeneratePlaneTunnelService(wrkCtx typ.WorkerContext, runtime *controlplanev1alpha1.Runtime) ([]corev1.Service, error) {
	selectorLabels := map[string]string{
		"app.kubernetes.io/name":       PlaneTunnelName(runtime.Name),
		"app.kubernetes.io/managed-by": "heir",
	}
	svcSpec := runtime.Spec.ControlPlane.Service
	tunnelPort := int32(wrkCtx.PlaneTunnelServerServer)
	egressPort := int32(wrkCtx.PlaneTunnelServerEgressSelectorPort)

	// Headless service for DNS-based replica discovery (--replica-discovery-dns).
	// Exposes the tunnel listener port only; the agent resolves this name to count
	// how many server replicas are running.
	headless := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlaneTunnelHeadlessName(runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    selectorLabels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "tunnel",
					Port:       tunnelPort,
					TargetPort: intstr.FromInt32(8443),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Tunnel service for worker agent connections — external, so ServiceType is
	// inherited from the control-plane service.
	tunnel := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlaneTunnelName(runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    selectorLabels,
		},
		Spec: corev1.ServiceSpec{
			Type:     runtime.Spec.ControlPlane.Service.ServiceType,
			Selector: selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "tunnel",
					Port:       tunnelPort,
					TargetPort: intstr.FromInt32(tunnelPort),
					Protocol:   corev1.ProtocolTCP,
					NodePort:   svcSpec.PlaneTunnelNodePort,
				},
			},
		},
	}

	// Egress-selector service for the API server, always ClusterIP
	egressSelector := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlaneTunnelEgressName(runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    selectorLabels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "egress-selector",
					Port:       egressPort,
					TargetPort: intstr.FromInt32(egressPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	return []corev1.Service{headless, tunnel, egressSelector}, nil
}

func GeneratePlaneTunnelDeployment(wrkCtx typ.WorkerContext, runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) *appsv1.Deployment {
	spec := runtime.Spec.ControlPlane.PlaneTunnel.Server
	deploySpec := spec.Deployment

	labels := map[string]string{
		"app.kubernetes.io/name":       PlaneTunnelName(runtime.Name),
		"app.kubernetes.io/managed-by": "heir",
	}
	podLabels := mergeMaps(labels, deploySpec.AdditionalMetadata.Labels)

	headlessFQDN := fmt.Sprintf("%s.%s.svc.cluster.local", PlaneTunnelHeadlessName(runtime.Name), runtime.Namespace)

	args := []string{
		"server",
		"--tunnel-cert", layout.PKI.PlaneTunnelCert.MountPath,
		"--tunnel-key", layout.PKI.PlaneTunnelKey.MountPath,
		"--tunnel-ca-cert", layout.PKI.CACert.MountPath,
		"--tunnel-addr", fmt.Sprintf(":%d", wrkCtx.PlaneTunnelServerServer),
		"--egress-cert", layout.PKI.ApiServerPlaneTunnelCert.MountPath,
		"--egress-key", layout.PKI.ApiServerPlaneTunnelKey.MountPath,
		"--egress-ca-cert", layout.PKI.CACert.MountPath,
		"--egress-addr", fmt.Sprintf(":%d", wrkCtx.PlaneTunnelServerEgressSelectorPort),
		"--replica-discovery-dns", headlessFQDN,
	}

	volumes := []corev1.Volume{
		{
			Name: "pki",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: runtime.Name},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{Name: "pki", MountPath: layout.PKI.CACert.MountPath, SubPath: layout.PKI.CACert.SecretKey, ReadOnly: true},
		{Name: "pki", MountPath: layout.PKI.PlaneTunnelCert.MountPath, SubPath: layout.PKI.PlaneTunnelCert.SecretKey, ReadOnly: true},
		{Name: "pki", MountPath: layout.PKI.PlaneTunnelKey.MountPath, SubPath: layout.PKI.PlaneTunnelKey.SecretKey, ReadOnly: true},
		{Name: "pki", MountPath: layout.PKI.ApiServerPlaneTunnelCert.MountPath, SubPath: layout.PKI.ApiServerPlaneTunnelCert.SecretKey, ReadOnly: true},
		{Name: "pki", MountPath: layout.PKI.ApiServerPlaneTunnelKey.MountPath, SubPath: layout.PKI.ApiServerPlaneTunnelKey.SecretKey, ReadOnly: true},
	}

	var resources corev1.ResourceRequirements
	if deploySpec.Resources != nil {
		resources = *deploySpec.Resources
	}

	var runtimeClassName *string
	if deploySpec.RuntimeClassName != "" {
		runtimeClassName = &deploySpec.RuntimeClassName
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PlaneTunnelName(runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: deploySpec.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: deploySpec.AdditionalMetadata.Annotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: deploySpec.ServiceAccountName,
					RuntimeClassName:   runtimeClassName,
					Tolerations:        deploySpec.Tolerations,
					Affinity:           deploySpec.Affinity,
					Volumes:            volumes,
					Containers: []corev1.Container{
						{
							Name:  "tunnel-server",
							Image: spec.Image,
							Args:  args,
							Ports: []corev1.ContainerPort{
								{Name: "tunnel", ContainerPort: int32(wrkCtx.PlaneTunnelServerServer), Protocol: corev1.ProtocolTCP},
								{Name: "egress-selector", ContainerPort: int32(wrkCtx.PlaneTunnelServerEgressSelectorPort), Protocol: corev1.ProtocolTCP},
							},
							VolumeMounts: volumeMounts,
							Resources:    resources,
						},
					},
				},
			},
		},
	}
}
