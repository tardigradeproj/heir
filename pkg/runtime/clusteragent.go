package runtime

import (
	"fmt"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// ClusterAgentRuntimeHashAnnotation is the pod-template annotation that tracks the hash of
// the cluster-agent runtime Secret's content, so a change to the mounted Runtime manifest
// rolls the clusteragent Deployment.
const ClusterAgentRuntimeHashAnnotation = "heir.tardigrade.runtime.io/cluster-agent-runtime-hash"

// GenerateClusterAgentDeployment builds the clusteragent Deployment for the given Runtime.
// clusteragent always runs a single replica: it mints worker bootstrap tokens for this
// Runtime's tenant cluster, and running more than one would race on token issuance. No API
// calls are made; the caller is responsible for setting the owner reference and persisting
// the result.
func GenerateClusterAgentDeployment(
	runtime *controlplanev1alpha1.Runtime,
	layout ControlPlaneLayout,
	opts ...DeployOpts,
) (*appsv1.Deployment, error) {
	deployOps := deploymentOpts{}
	for _, opt := range opts {
		opt(&deployOps)
	}
	deploySpec := runtime.Spec.ControlPlane.Deployment
	labels := map[string]string{
		"app.kubernetes.io/name":       fmt.Sprintf("%s-cluster-agent", runtime.Name),
		"app.kubernetes.io/managed-by": "heir",
	}
	podLabels := mergeMaps(labels, deploySpec.AdditionalMetadata.Labels)
	podAnnotations := mergeMaps(
		deployOps.annotations,
		deploySpec.AdditionalMetadata.Annotations,
	)
	volumes := []corev1.Volume{
		{
			Name: "pki-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: runtime.Name,
				},
			},
		},
		{
			Name: "runtime-manifest",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ClusterAgentRuntimeSecretName(runtime.Name),
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		// Auth: one subPath mount per kubeconfig so the rest of /etc/kubernetes is unaffected.
		{Name: "pki-auth", MountPath: layout.Auth.AdminConf.MountPath, SubPath: layout.Auth.AdminConf.SecretKey, ReadOnly: true},
		{Name: "pki-auth", MountPath: layout.PKI.CACert.MountPath, SubPath: layout.PKI.CACert.SecretKey, ReadOnly: true},
		{Name: "runtime-manifest", MountPath: layout.ClusterAgent.RuntimeManifest.MountPath, SubPath: layout.ClusterAgent.RuntimeManifest.SecretKey, ReadOnly: true},
	}

	var runtimeClassName *string
	if deploySpec.RuntimeClassName != "" {
		runtimeClassName = &deploySpec.RuntimeClassName
	}

	containers := []corev1.Container{
		{
			Name:         "clusteragent",
			Image:        runtime.Spec.ControlPlane.ClusterAgent.Image,
			Command:      []string{"/clusteragent"},
			VolumeMounts: volumeMounts,
			Env: []corev1.EnvVar{
				{Name: "KUBECONFIG", Value: layout.Auth.AdminConf.MountPath},
			},
		},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cluster-agent", runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: new(int32(1)),
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
					Containers:         containers,
					Volumes:            volumes,
				},
			},
		},
	}, nil
}

// ClusterAgentRuntimeSecretName returns the name of the Secret holding the Status-stripped
// Runtime manifest that GenerateClusterAgentDeployment mounts into the clusteragent
// container.
func ClusterAgentRuntimeSecretName(runtimeName string) string {
	return fmt.Sprintf("%s-cluster-agent-runtime", runtimeName)
}

// GenerateClusterAgentRuntimeSecret marshals runtime to YAML, excluding its Status field,
// and returns it as the Secret GenerateClusterAgentDeployment's "runtime-manifest" volume
// expects at layout.ClusterAgent.RuntimeManifest.SecretKey. No API calls are made; the
// caller is responsible for setting the owner reference and persisting the result.
func GenerateClusterAgentRuntimeSecret(runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) (*corev1.Secret, error) {
	manifest := runtime.DeepCopy()
	manifest.Status = controlplanev1alpha1.RuntimeStatus{}
	manifest.TypeMeta = metav1.TypeMeta{
		APIVersion: controlplanev1alpha1.GroupVersion.String(),
		Kind:       "Runtime",
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runtime manifest: %w", err)
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       fmt.Sprintf("%s-cluster-agent", runtime.Name),
		"app.kubernetes.io/managed-by": "heir",
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClusterAgentRuntimeSecretName(runtime.Name),
			Namespace: runtime.Namespace,
			Labels:    labels,
		},
		Data: map[string][]byte{
			layout.ClusterAgent.RuntimeManifest.SecretKey: data,
		},
	}, nil
}
