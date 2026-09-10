package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func clusterAgentRuntime(name, namespace string, cp controlplanev1alpha1.ControlPlaneSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: controlplanev1alpha1.RuntimeSpec{
			ControlPlane: cp,
		},
	}
}

func TestClusterAgentRuntimeSecretName(t *testing.T) {
	tests := []struct {
		name        string
		runtimeName string
		want        string
	}{
		{name: "simple name", runtimeName: "my-cluster", want: "my-cluster-cluster-agent-runtime"},
		{name: "empty name", runtimeName: "", want: "-cluster-agent-runtime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClusterAgentRuntimeSecretName(tt.runtimeName))
		})
	}
}

func TestGenerateClusterAgentDeployment(t *testing.T) {
	layout := NewControlPlaneLayout()

	tests := []struct {
		name     string
		runtime  *controlplanev1alpha1.Runtime
		opts     []DeployOpts
		validate func(t *testing.T, deploy *appsv1.Deployment)
	}{
		{
			name: "deployment name, namespace, and labels match the runtime",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Equal(t, "my-cluster-cluster-agent", deploy.Name)
				assert.Equal(t, "default", deploy.Namespace)
				assert.Equal(t, "my-cluster-cluster-agent", deploy.Labels["app.kubernetes.io/name"])
				assert.Equal(t, "heir", deploy.Labels["app.kubernetes.io/managed-by"])
			},
		},
		{
			name: "always runs a single replica regardless of DeploymentSpec.Replicas",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				Deployment:   controlplanev1alpha1.DeploymentSpec{Replicas: new(int32(5))},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.NotNil(t, deploy.Spec.Replicas)
				assert.Equal(t, int32(1), *deploy.Spec.Replicas)
			},
		},
		{
			name: "selector matches pod template base labels",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.NotNil(t, deploy.Spec.Selector)
				assert.Equal(t, deploy.Labels, deploy.Spec.Selector.MatchLabels)
				assert.Subset(t, deploy.Spec.Template.Labels, deploy.Spec.Selector.MatchLabels)
			},
		},
		{
			name: "container image comes from ControlPlane.ClusterAgent.Image",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v2"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
				assert.Equal(t, "ghcr.io/example/clusteragent:v2", deploy.Spec.Template.Spec.Containers[0].Image)
			},
		},
		{
			name: "container runs the /clusteragent binary",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
				assert.Equal(t, []string{"/clusteragent"}, deploy.Spec.Template.Spec.Containers[0].Command)
			},
		},
		{
			name: "KUBECONFIG env var points at the mounted admin.conf path",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
				assert.Contains(t, deploy.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{
					Name: "KUBECONFIG", Value: layout.Auth.AdminConf.MountPath,
				})
			},
		},
		{
			name: "pki-auth volume sources from the runtime's own PKI secret",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				vol := findVolume(t, deploy, "pki-auth")
				require.NotNil(t, vol.Secret)
				assert.Equal(t, "my-cluster", vol.Secret.SecretName)
			},
		},
		{
			name: "runtime-manifest volume sources from the cluster agent runtime secret",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				vol := findVolume(t, deploy, "runtime-manifest")
				require.NotNil(t, vol.Secret)
				assert.Equal(t, ClusterAgentRuntimeSecretName("my-cluster"), vol.Secret.SecretName)
			},
		},
		{
			name: "admin kubeconfig, CA cert, and runtime manifest are each mounted at their layout paths",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
				mounts := deploy.Spec.Template.Spec.Containers[0].VolumeMounts
				assert.Contains(t, mounts, corev1.VolumeMount{
					Name: "pki-auth", MountPath: layout.Auth.AdminConf.MountPath, SubPath: layout.Auth.AdminConf.SecretKey, ReadOnly: true,
				})
				assert.Contains(t, mounts, corev1.VolumeMount{
					Name: "pki-auth", MountPath: layout.PKI.CACert.MountPath, SubPath: layout.PKI.CACert.SecretKey, ReadOnly: true,
				})
				assert.Contains(t, mounts, corev1.VolumeMount{
					Name: "runtime-manifest", MountPath: layout.ClusterAgent.RuntimeManifest.MountPath, SubPath: layout.ClusterAgent.RuntimeManifest.SecretKey, ReadOnly: true,
				})
			},
		},
		{
			name: "runtime class name is nil when unset",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Nil(t, deploy.Spec.Template.Spec.RuntimeClassName)
			},
		},
		{
			name: "runtime class name is propagated when set",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				Deployment:   controlplanev1alpha1.DeploymentSpec{RuntimeClassName: "gvisor"},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				require.NotNil(t, deploy.Spec.Template.Spec.RuntimeClassName)
				assert.Equal(t, "gvisor", *deploy.Spec.Template.Spec.RuntimeClassName)
			},
		},
		{
			name: "service account name, tolerations, and affinity propagate from DeploymentSpec",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				Deployment: controlplanev1alpha1.DeploymentSpec{
					ServiceAccountName: "clusteragent-sa",
					Tolerations: []corev1.Toleration{
						{Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "clusteragent", Effect: corev1.TaintEffectNoSchedule},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{Key: "kubernetes.io/os", Operator: corev1.NodeSelectorOpIn, Values: []string{"linux"}},
									},
								}},
							},
						},
					},
				},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Equal(t, "clusteragent-sa", deploy.Spec.Template.Spec.ServiceAccountName)
				require.Len(t, deploy.Spec.Template.Spec.Tolerations, 1)
				assert.Equal(t, "dedicated", deploy.Spec.Template.Spec.Tolerations[0].Key)
				require.NotNil(t, deploy.Spec.Template.Spec.Affinity)
				require.NotNil(t, deploy.Spec.Template.Spec.Affinity.NodeAffinity)
			},
		},
		{
			name: "additional metadata labels and annotations land on the pod template",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				Deployment: controlplanev1alpha1.DeploymentSpec{
					AdditionalMetadata: controlplanev1alpha1.AdditionalMetadata{
						Labels:      map[string]string{"env": "prod"},
						Annotations: map[string]string{"custom/annotation": "value"},
					},
				},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Equal(t, "prod", deploy.Spec.Template.Labels["env"])
				assert.Equal(t, "value", deploy.Spec.Template.Annotations["custom/annotation"])
			},
		},
		{
			name: "additional metadata labels do not override the selector",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				Deployment: controlplanev1alpha1.DeploymentSpec{
					AdditionalMetadata: controlplanev1alpha1.AdditionalMetadata{
						Labels: map[string]string{"app.kubernetes.io/name": "overridden"},
					},
				},
			}),
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Equal(t, "my-cluster-cluster-agent", deploy.Spec.Selector.MatchLabels["app.kubernetes.io/name"])
			},
		},
		{
			name: "WithAnnotation options land on the pod template",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			opts: []DeployOpts{WithAnnotation(ClusterAgentRuntimeHashAnnotation, "deadbeef")},
			validate: func(t *testing.T, deploy *appsv1.Deployment) {
				assert.Equal(t, "deadbeef", deploy.Spec.Template.Annotations[ClusterAgentRuntimeHashAnnotation])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deploy, err := GenerateClusterAgentDeployment(tt.runtime, layout, tt.opts...)
			require.NoError(t, err)
			require.NotNil(t, deploy)
			tt.validate(t, deploy)
		})
	}
}

// findVolume returns the volume named volName in deploy, failing the test if absent.
func findVolume(t *testing.T, deploy *appsv1.Deployment, volName string) corev1.Volume {
	t.Helper()
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == volName {
			return v
		}
	}
	require.Failf(t, "volume not found", "expected a volume named %q", volName)
	return corev1.Volume{}
}

func TestGenerateClusterAgentRuntimeSecret(t *testing.T) {
	layout := NewControlPlaneLayout()

	tests := []struct {
		name     string
		runtime  *controlplanev1alpha1.Runtime
		validate func(t *testing.T, secret *corev1.Secret, manifest map[string]interface{})
	}{
		{
			name: "secret name, namespace, and labels match the runtime",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, secret *corev1.Secret, _ map[string]interface{}) {
				assert.Equal(t, "my-cluster-cluster-agent-runtime", secret.Name)
				assert.Equal(t, "default", secret.Namespace)
				assert.Equal(t, "my-cluster-cluster-agent", secret.Labels["app.kubernetes.io/name"])
				assert.Equal(t, "heir", secret.Labels["app.kubernetes.io/managed-by"])
			},
		},
		{
			name: "manifest is stored under the layout's secret key and is non-empty",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, secret *corev1.Secret, _ map[string]interface{}) {
				require.Contains(t, secret.Data, layout.ClusterAgent.RuntimeManifest.SecretKey)
				assert.NotEmpty(t, secret.Data[layout.ClusterAgent.RuntimeManifest.SecretKey])
			},
		},
		{
			name: "manifest carries apiVersion and kind",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, _ *corev1.Secret, manifest map[string]interface{}) {
				assert.Equal(t, controlplanev1alpha1.GroupVersion.String(), manifest["apiVersion"])
				assert.Equal(t, "Runtime", manifest["kind"])
			},
		},
		{
			name: "status field is excluded from the marshaled manifest even when non-zero on the runtime",
			runtime: func() *controlplanev1alpha1.Runtime {
				rt := clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
					ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
				})
				rt.Status = controlplanev1alpha1.RuntimeStatus{
					ObservedGeneration: 5,
					Conditions:         []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "ok"}},
				}
				return rt
			}(),
			validate: func(t *testing.T, _ *corev1.Secret, manifest map[string]interface{}) {
				_, hasStatus := manifest["status"]
				assert.False(t, hasStatus, "status field must not appear in the mounted manifest")
			},
		},
		{
			name: "spec content round-trips through the marshaled manifest",
			runtime: clusterAgentRuntime("my-cluster", "default", controlplanev1alpha1.ControlPlaneSpec{
				ClusterAgent: controlplanev1alpha1.ClusterAgentSpec{Image: "ghcr.io/example/clusteragent:v1"},
			}),
			validate: func(t *testing.T, secret *corev1.Secret, _ map[string]interface{}) {
				roundTripped := &controlplanev1alpha1.Runtime{}
				require.NoError(t, yaml.Unmarshal(secret.Data[layout.ClusterAgent.RuntimeManifest.SecretKey], roundTripped))
				assert.Equal(t, "ghcr.io/example/clusteragent:v1", roundTripped.Spec.ControlPlane.ClusterAgent.Image)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := GenerateClusterAgentRuntimeSecret(tt.runtime, layout)
			require.NoError(t, err)
			require.NotNil(t, secret)

			var manifest map[string]interface{}
			require.NoError(t, yaml.Unmarshal(secret.Data[layout.ClusterAgent.RuntimeManifest.SecretKey], &manifest))

			tt.validate(t, secret, manifest)
		})
	}
}
