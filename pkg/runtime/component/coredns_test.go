package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/utils/ptr"
	sigsyaml "sigs.k8s.io/yaml"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
)

func corednsRuntime(spec controlplanev1alpha1.CorednsSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			Cluster: controlplanev1alpha1.ClusterSpec{
				Network: controlplanev1alpha1.NetworkSpec{
					Coredns: spec,
				},
			},
		},
	}
}

func TestCreateCorednsManifest(t *testing.T) {
	tests := []struct {
		name     string
		spec     controlplanev1alpha1.CorednsSpec
		validate func(t *testing.T, resources map[string][]byte)
	}{
		{
			name: "all expected resource kinds are present",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas: ptr.To(int32(2)),
				RegistrySettings: controlplanev1alpha1.RegistrySettings{
					Registry: "registry.k8s.io",
					Image:    "coredns/coredns:v1.12.1",
				},
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				for _, key := range []string{
					"ServiceAccount/coredns",
					"ClusterRole/system-coredns",
					"ClusterRoleBinding/system-coredns",
					"ConfigMap/coredns",
					"Deployment/coredns",
					"Service/kube-dns",
				} {
					assert.Contains(t, resources, key, "missing resource %s", key)
				}
			},
		},
		{
			name: "service account is in kube-system",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(2)),
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var sa corev1.ServiceAccount
				require.NoError(t, sigsyaml.Unmarshal(resources["ServiceAccount/coredns"], &sa))
				assert.Equal(t, "coredns", sa.Name)
				assert.Equal(t, "kube-system", sa.Namespace)
			},
		},
		{
			name: "cluster role binding references system-coredns service account",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(2)),
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var crb rbacv1.ClusterRoleBinding
				require.NoError(t, sigsyaml.Unmarshal(resources["ClusterRoleBinding/system-coredns"], &crb))
				assert.Equal(t, "system-coredns", crb.RoleRef.Name)
				assert.Equal(t, "ClusterRole", crb.RoleRef.Kind)
				require.Len(t, crb.Subjects, 1)
				assert.Equal(t, "coredns", crb.Subjects[0].Name)
				assert.Equal(t, "kube-system", crb.Subjects[0].Namespace)
			},
		},
		{
			name: "deployment uses image from registry settings",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas: ptr.To(int32(2)),
				RegistrySettings: controlplanev1alpha1.RegistrySettings{
					Registry:   "my.registry.io",
					Image:      "coredns/coredns:v1.11.0",
					PullPolicy: corev1.PullAlways,
				},
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var dep appsv1.Deployment
				require.NoError(t, sigsyaml.Unmarshal(resources["Deployment/coredns"], &dep))
				container := dep.Spec.Template.Spec.Containers[0]
				assert.Equal(t, "my.registry.io/coredns/coredns:v1.11.0", container.Image)
				assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
			},
		},
		{
			name: "deployment replica count matches spec",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(3)),
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var dep appsv1.Deployment
				require.NoError(t, sigsyaml.Unmarshal(resources["Deployment/coredns"], &dep))
				require.NotNil(t, dep.Spec.Replicas)
				assert.Equal(t, int32(3), *dep.Spec.Replicas)
			},
		},
		{
			name: "service uses cluster DNS IP as clusterIP",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(2)),
				ClusterDNSIP: "10.96.0.53",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var svc corev1.Service
				require.NoError(t, sigsyaml.Unmarshal(resources["Service/kube-dns"], &svc))
				assert.Equal(t, "10.96.0.53", svc.Spec.ClusterIP)
			},
		},
		{
			name: "configmap contains cluster domain in Corefile",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(2)),
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var cm corev1.ConfigMap
				require.NoError(t, sigsyaml.Unmarshal(resources["ConfigMap/coredns"], &cm))
				assert.Contains(t, cm.Data["Corefile"], "cluster.local")
			},
		},
		{
			name: "single replica sets maxUnavailable to 0",
			spec: controlplanev1alpha1.CorednsSpec{
				Replicas:     ptr.To(int32(1)),
				ClusterDNSIP: "10.96.0.10",
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var dep appsv1.Deployment
				require.NoError(t, sigsyaml.Unmarshal(resources["Deployment/coredns"], &dep))
				require.NotNil(t, dep.Spec.Strategy.RollingUpdate)
				assert.Equal(t, "0", dep.Spec.Strategy.RollingUpdate.MaxUnavailable.String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := corednsRuntime(tt.spec)

			manifest, err := CreateCorednsManifest(runtime)
			require.NoError(t, err)
			require.NotEmpty(t, manifest)

			resources := parseManifest(t, manifest)
			tt.validate(t, resources)
		})
	}
}
