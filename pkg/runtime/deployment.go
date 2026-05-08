package runtime

import (
	"fmt"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GenerateDeployment builds the control-plane Deployment for the given Runtime and config hash.
// No API calls are made; the caller is responsible for setting the owner reference and persisting
// the result.
func GenerateDeployment(runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout, configHash string) (*appsv1.Deployment, error) {
	deploySpec := runtime.Spec.ControlPlane.Deployment
	samaritano := runtime.Spec.ControlPlane.Samaritano

	labels := map[string]string{
		"app.kubernetes.io/name":       runtime.Name,
		"app.kubernetes.io/managed-by": "samaritano",
	}

	podLabels := mergeMaps(labels, deploySpec.AdditionalMetadata.Labels)
	podAnnotations := mergeMaps(
		map[string]string{"samaritano.tardigrade.runtime.io/s6-overlay-config-hash": configHash},
		deploySpec.AdditionalMetadata.Annotations,
	)

	containerPorts := deploymentPorts(runtime.Spec.ControlPlane.Service.AdditionalPorts)

	// s6-overlay run-scripts need to be executable; 0755 is applied to the whole volume.
	scriptMode := int32(0755)

	volumes := []corev1.Volume{
		{
			Name: "pki-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: fmt.Sprintf("%s-pki-auth", runtime.Name),
				},
			},
		},
		{
			Name: "storage",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  fmt.Sprintf("%s-storage", runtime.Name),
					DefaultMode: &scriptMode,
				},
			},
		},
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-config", runtime.Name),
					},
					DefaultMode: &scriptMode,
				},
			},
		},
		{
			Name: "static-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-config", runtime.Name),
					},
					DefaultMode: &scriptMode,
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		// PKI: mount the whole secret directory — all certs/keys land at /etc/kubernetes/pki/<file>.
		{Name: "pki-auth", MountPath: "/etc/kubernetes/pki", ReadOnly: true},
		// Auth: one subPath mount per kubeconfig so the rest of /etc/kubernetes is unaffected.
		{Name: "pki-auth", MountPath: layout.Auth.AdminConf.MountPath, SubPath: layout.Auth.AdminConf.SecretKey, ReadOnly: true},
		{Name: "pki-auth", MountPath: layout.Auth.ControllerManagerConf.MountPath, SubPath: layout.Auth.ControllerManagerConf.SecretKey, ReadOnly: true},
		{Name: "pki-auth", MountPath: layout.Auth.SchedulerConf.MountPath, SubPath: layout.Auth.SchedulerConf.SecretKey, ReadOnly: true},
		// Storage: run-script from the storage Secret.
		{Name: "storage", MountPath: layout.Storage.Script.MountPath, SubPath: layout.Storage.Script.SecretKey, ReadOnly: true},
		// Config: one subPath mount per s6 run-script.
		{Name: "config", MountPath: layout.Config.APIServer.MountPath, SubPath: layout.Config.APIServer.SecretKey, ReadOnly: true},
		{Name: "config", MountPath: layout.Config.ControllerManager.MountPath, SubPath: layout.Config.ControllerManager.SecretKey, ReadOnly: true},
		{Name: "config", MountPath: layout.Config.Scheduler.MountPath, SubPath: layout.Config.Scheduler.SecretKey, ReadOnly: true},
		// mount static configs
		{Name: "static-config", MountPath: layout.StaticManifest.Bootstrap.MountPath, SubPath: layout.StaticManifest.Bootstrap.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.KubeProxy.MountPath, SubPath: layout.StaticManifest.KubeProxy.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.Coredns.MountPath, SubPath: layout.StaticManifest.Coredns.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.NodeProfile.MountPath, SubPath: layout.StaticManifest.NodeProfile.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.FlannelCNI.MountPath, SubPath: layout.StaticManifest.FlannelCNI.SecretKey, ReadOnly: true},
	}

	var runtimeClassName *string
	if deploySpec.RuntimeClassName != "" {
		runtimeClassName = &deploySpec.RuntimeClassName
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtime.Name,
			Namespace: runtime.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: deploySpec.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: deploySpec.ServiceAccountName,
					RuntimeClassName:   runtimeClassName,
					Tolerations:        deploySpec.Tolerations,
					Affinity:           deploySpec.Affinity,
					Containers: []corev1.Container{
						{
							Name:         "samaritano",
							Image:        samaritano.Image,
							Ports:        containerPorts,
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}, nil
}

// deploymentPorts converts AdditionalPort entries from the spec into corev1.ContainerPort values
// and appends the default apiserver port.
func deploymentPorts(ports []controlplanev1alpha1.AdditionalPort) []corev1.ContainerPort {
	out := make([]corev1.ContainerPort, 0, len(ports)+1)
	for _, p := range ports {
		out = append(out, corev1.ContainerPort{
			Name:          p.Name,
			ContainerPort: p.Port,
			Protocol:      p.Protocol,
		})
	}
	out = append(out, corev1.ContainerPort{Name: "apiserver", ContainerPort: 6443, Protocol: corev1.ProtocolTCP})
	return out
}

// mergeMaps returns a new map containing all keys from base overridden by extra.
func mergeMaps(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
