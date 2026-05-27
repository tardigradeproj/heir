package runtime

import (
	"fmt"
	"path/filepath"
	"sort"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
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
	wrkCtx := typ.NewWorkerContextWithDefaults()
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
	storage := runtime.Spec.UpstreamCluster.Storage
	var env []corev1.EnvVar
	if storage.Type == "kine" {
		if storage.Kine != nil && storage.Kine.DataSourceRef.Name != "" {
			env = append(env, corev1.EnvVar{
				Name: "SAMARITANO_STORAGE_ENDPOINT",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: storage.Kine.DataSourceRef.Name,
						},
						Key: storage.Kine.DataSourceRef.Key,
					},
				},
			})
		}
	}
	volumeMounts := []corev1.VolumeMount{
		// PKI: mount the whole secret directory — all certs/keys land at /etc/kubernetes/pki/<file>.
		{Name: "pki-auth", MountPath: "/etc/kubernetes/pki", ReadOnly: true},
		// Auth: one subPath mount per kubeconfig so the rest of /etc/kubernetes is unaffected.
		{Name: "pki-auth", MountPath: layout.Auth.AdminConf.MountPath, SubPath: layout.Auth.AdminConf.SecretKey, ReadOnly: true},
		{Name: "pki-auth", MountPath: layout.Auth.ControllerManagerConf.MountPath, SubPath: layout.Auth.ControllerManagerConf.SecretKey, ReadOnly: true},
		{Name: "pki-auth", MountPath: layout.Auth.SchedulerConf.MountPath, SubPath: layout.Auth.SchedulerConf.SecretKey, ReadOnly: true},
		// Config: one subPath mount per s6 run-script.
		{Name: "config", MountPath: layout.Config.APIServer.MountPath, SubPath: layout.Config.APIServer.SecretKey, ReadOnly: true},
		{Name: "config", MountPath: layout.Config.ControllerManager.MountPath, SubPath: layout.Config.ControllerManager.SecretKey, ReadOnly: true},
		{Name: "config", MountPath: layout.Config.Scheduler.MountPath, SubPath: layout.Config.Scheduler.SecretKey, ReadOnly: true},
		// mount static configs
		{Name: "static-config", MountPath: layout.StaticManifest.Bootstrap.MountPath, SubPath: layout.StaticManifest.Bootstrap.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.KubeProxy.MountPath, SubPath: layout.StaticManifest.KubeProxy.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.Coredns.MountPath, SubPath: layout.StaticManifest.Coredns.SecretKey, ReadOnly: true},
		{Name: "static-config", MountPath: layout.StaticManifest.NodeProfile.MountPath, SubPath: layout.StaticManifest.NodeProfile.SecretKey, ReadOnly: true},
	}
	if runtime.Spec.UpstreamCluster.Network.CNI.Supplier == "flannel" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name: "static-config", MountPath: layout.StaticManifest.FlannelCNI.MountPath, SubPath: layout.StaticManifest.FlannelCNI.SecretKey, ReadOnly: true},
		)
	}

	if runtime.Spec.UpstreamCluster.Network.Konnectivity.Enabled {
		volumes = append(volumes, corev1.Volume{
			Name:         "konnectivity-uds",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
		volumeMounts = append(volumeMounts,
			corev1.VolumeMount{Name: "config", MountPath: layout.Config.Konnectivity.MountPath, SubPath: layout.Config.Konnectivity.SecretKey, ReadOnly: true},
			corev1.VolumeMount{Name: "konnectivity-uds", MountPath: filepath.Dir(wrkCtx.KonnectivityUdsName)},
			corev1.VolumeMount{Name: "static-config", MountPath: layout.StaticManifest.KonnectivityAgent.MountPath, SubPath: layout.StaticManifest.KonnectivityAgent.SecretKey, ReadOnly: true},
		)
	}
	var runtimeClassName *string
	if deploySpec.RuntimeClassName != "" {
		runtimeClassName = &deploySpec.RuntimeClassName
	}

	containers := []corev1.Container{
		{
			Name:         "samaritano",
			Image:        samaritano.Image,
			Ports:        containerPorts,
			VolumeMounts: volumeMounts,
			Env:          env,
		},
	}
	if runtime.Spec.UpstreamCluster.Network.Konnectivity.Enabled {
		containers = append(containers, konnectivitySidecar(runtime.Spec.UpstreamCluster.Network.Konnectivity.KonnectivityServerSpec, layout, wrkCtx))
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
					Containers:         containers,
					Volumes:            volumes,
				},
			},
		},
	}, nil
}

// konnectivitySidecar builds the konnectivity-server container that shares the UDS socket
// volume with the samaritano container. The UDS socket is used by the API server egress
// selector to proxy traffic to worker nodes via the konnectivity-agent.
func konnectivitySidecar(spec controlplanev1alpha1.KonnectivityServerSpec, layout ControlPlaneLayout, wrkCtx *typ.WorkerContext) corev1.Container {
	udsDir := filepath.Dir(wrkCtx.KonnectivityUdsName)

	defaults := map[string]string{
		"logtostderr":             "true",
		"uds-name":                wrkCtx.KonnectivityUdsName,
		"cluster-cert":            layout.PKI.APIServerCert.MountPath,
		"cluster-key":             layout.PKI.APIServerKey.MountPath,
		"server-port":             "0",
		"agent-port":              fmt.Sprintf("%d", wrkCtx.KonnectivityProxyServerPort),
		"health-port":             "8134",
		"admin-port":              "8133",
		"mode":                    "grpc",
		"agent-namespace":         "kube-system",
		"agent-service-account":   "konnectivity-agent",
		"kubeconfig":              layout.Auth.KonnectivityConf.MountPath,
		"authentication-audience": "system:konnectivity-server",
	}
	merged := MergeArgs(defaults, spec.ExtraArgs)

	keys := make([]string, 0, len(merged))
	for k, _ := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(merged))
	for _, k := range keys {
		args = append(args, fmt.Sprintf("--%s=%s", k, merged[k]))
	}
	c := corev1.Container{
		Name:    "konnectivity-server",
		Image:   spec.Image,
		Command: []string{"/proxy-server"},
		Args:    args,
		Ports: []corev1.ContainerPort{
			{Name: "agent", ContainerPort: wrkCtx.KonnectivityProxyServerPort, Protocol: corev1.ProtocolTCP},
			{Name: "healthport", ContainerPort: 8134, Protocol: corev1.ProtocolTCP},
			{Name: "adminport", ContainerPort: 8133, Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "pki-auth", MountPath: "/etc/kubernetes/pki", ReadOnly: true},
			{Name: "pki-auth", MountPath: layout.Auth.KonnectivityConf.MountPath, SubPath: layout.Auth.KonnectivityConf.SecretKey, ReadOnly: true},
			{Name: "konnectivity-uds", MountPath: udsDir},
		},
	}
	if spec.Resources != nil {
		c.Resources = *spec.Resources
	}
	return c
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
