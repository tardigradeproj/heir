package runtime

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func helmChart(name, namespace string) *clusteragentv1alpha1.HelmChart {
	return &clusteragentv1alpha1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func TestGenerateRBAC(t *testing.T) {
	tests := []struct {
		name     string
		chart    *clusteragentv1alpha1.HelmChart
		validate func(t *testing.T, sa *corev1.ServiceAccount, crb *rbacv1.ClusterRoleBinding)
	}{
		{
			name:  "service account name is helmchart-<chart name>",
			chart: helmChart("podinfo", "podinfo"),
			validate: func(t *testing.T, sa *corev1.ServiceAccount, _ *rbacv1.ClusterRoleBinding) {
				assert.Equal(t, "helmchart-podinfo", sa.Name)
			},
		},
		{
			name:  "service account namespace matches the HelmChart's own namespace",
			chart: helmChart("podinfo", "podinfo"),
			validate: func(t *testing.T, sa *corev1.ServiceAccount, _ *rbacv1.ClusterRoleBinding) {
				assert.Equal(t, "podinfo", sa.Namespace)
			},
		},
		{
			name:  "clusterrolebinding name matches the service account name",
			chart: helmChart("podinfo", "podinfo"),
			validate: func(t *testing.T, sa *corev1.ServiceAccount, crb *rbacv1.ClusterRoleBinding) {
				assert.Equal(t, sa.Name, crb.Name)
			},
		},
		{
			name:  "clusterrolebinding binds to the built-in cluster-admin ClusterRole",
			chart: helmChart("podinfo", "podinfo"),
			validate: func(t *testing.T, _ *corev1.ServiceAccount, crb *rbacv1.ClusterRoleBinding) {
				assert.Equal(t, rbacv1.RoleRef{
					APIGroup: rbacv1.GroupName,
					Kind:     "ClusterRole",
					Name:     "cluster-admin",
				}, crb.RoleRef)
			},
		},
		{
			name:  "clusterrolebinding's sole subject is the generated service account",
			chart: helmChart("podinfo", "podinfo"),
			validate: func(t *testing.T, sa *corev1.ServiceAccount, crb *rbacv1.ClusterRoleBinding) {
				require.Len(t, crb.Subjects, 1)
				assert.Equal(t, rbacv1.Subject{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      sa.Name,
					Namespace: sa.Namespace,
				}, crb.Subjects[0])
			},
		},
		{
			name:  "chart name and HelmChart namespace can differ, and each object follows its own field",
			chart: helmChart("cert-manager", "cert-manager-system"),
			validate: func(t *testing.T, sa *corev1.ServiceAccount, crb *rbacv1.ClusterRoleBinding) {
				assert.Equal(t, "helmchart-cert-manager", sa.Name)
				assert.Equal(t, "cert-manager-system", sa.Namespace)
				assert.Equal(t, "helmchart-cert-manager", crb.Name)
				assert.Equal(t, "cert-manager-system", crb.Subjects[0].Namespace)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sa, crb := GenerateRBAC(tt.chart)
			require.NotNil(t, sa)
			require.NotNil(t, crb)
			tt.validate(t, sa, crb)
		})
	}
}

func TestCommand(t *testing.T) {
	tests := []struct {
		name      string
		chart     *clusteragentv1alpha1.HelmChart
		operation JobOperation
		want      [][]string
	}{
		{
			name: "teardown returns a single uninstall step",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{TargetNamespace: "podinfo"},
				},
			},
			operation: JobOperationTeardown,
			want: [][]string{
				{"helm", "uninstall", "podinfo", "--namespace", "podinfo"},
			},
		},
		{
			name: "teardown appends --timeout when helm.timeout is set",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{TargetNamespace: "podinfo"},
					Helm:  clusteragentv1alpha1.HelmOptions{Timeout: metav1.Duration{Duration: 2 * time.Minute}},
				},
			},
			operation: JobOperationTeardown,
			want: [][]string{
				{"helm", "uninstall", "podinfo", "--namespace", "podinfo", "--timeout", "2m0s"},
			},
		},
		{
			name: "teardown ignores chart.repo, values, and helm options other than timeout",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Helm:   clusteragentv1alpha1.HelmOptions{Atomic: true},
					Values: clusteragentv1alpha1.ValuesSpec{Set: map[string]string{"replicaCount": "2"}},
				},
			},
			operation: JobOperationTeardown,
			want: [][]string{
				{"helm", "uninstall", "podinfo", "--namespace", "podinfo"},
			},
		},
		{
			name: "install returns repo add then upgrade, in order",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo"},
			},
		},
		{
			name: "release name and chart name can differ; the chart ref is always <chart.name>/<chart.name>",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "my-release"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "cert-manager", Repo: "https://charts.jetstack.io", TargetNamespace: "cert-manager-system",
					},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "cert-manager", "https://charts.jetstack.io"},
				{"helm", "upgrade", "my-release", "cert-manager/cert-manager", "--install", "--namespace", "cert-manager-system"},
			},
		},
		{
			name: "createNamespace appends --create-namespace to the upgrade step only",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo", CreateNamespace: true,
					},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--create-namespace"},
			},
		},
		{
			name: "version appends --version to the upgrade step only",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo", Version: "6.15.0",
					},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--version", "6.15.0"},
			},
		},
		{
			name: "insecureSkipTLSVerify appends --insecure-skip-tls-verify to both repo add and upgrade",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Helm: clusteragentv1alpha1.HelmOptions{InsecureSkipTLSVerify: true},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts", "--insecure-skip-tls-verify"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--insecure-skip-tls-verify"},
			},
		},
		{
			name: "timeout appends the Go duration string to the upgrade step",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Helm: clusteragentv1alpha1.HelmOptions{Timeout: metav1.Duration{Duration: 90 * time.Second}},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--timeout", "1m30s"},
			},
		},
		{
			name: "atomic appends --atomic to the upgrade step",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Helm: clusteragentv1alpha1.HelmOptions{Atomic: true},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--atomic"},
			},
		},
		{
			name: "values.set flags are sorted by key for a deterministic command",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Values: clusteragentv1alpha1.ValuesSpec{Set: map[string]string{"zeta": "1", "alpha": "2"}},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{
					"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo",
					"--set", "alpha=2", "--set", "zeta=1",
				},
			},
		},
		{
			name: "values.content adds --values pointing at the fixed decode path",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
					},
					Values: clusteragentv1alpha1.ValuesSpec{Content: "replicaCount: 2"},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts"},
				{"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo", "--values", helmValuesFile},
			},
		},
		{
			name: "all install options combine in a fixed, predictable order",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{
						Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo",
						CreateNamespace: true, Version: "6.15.0",
					},
					Helm: clusteragentv1alpha1.HelmOptions{
						InsecureSkipTLSVerify: true,
						Timeout:               metav1.Duration{Duration: time.Minute},
						Atomic:                true,
					},
					Values: clusteragentv1alpha1.ValuesSpec{
						Set:     map[string]string{"replicaCount": "2"},
						Content: "extra: true",
					},
				},
			},
			operation: JobOperationInstall,
			want: [][]string{
				{"helm", "repo", "add", "podinfo", "https://example.com/charts", "--insecure-skip-tls-verify"},
				{
					"helm", "upgrade", "podinfo", "podinfo/podinfo", "--install", "--namespace", "podinfo",
					"--create-namespace", "--version", "6.15.0", "--insecure-skip-tls-verify",
					"--timeout", "1m0s", "--atomic", "--set", "replicaCount=2", "--values", helmValuesFile,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Command(tt.chart, tt.operation))
		})
	}
}

func TestGenerateJob(t *testing.T) {
	tests := []struct {
		name      string
		chart     *clusteragentv1alpha1.HelmChart
		operation JobOperation
		validate  func(t *testing.T, job *batchv1.Job)
	}{
		{
			name: "name is helmchart-<name>-<operation>",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.Equal(t, "helmchart-podinfo-install", job.Name)
			},
		},
		{
			name: "namespace matches the HelmChart's own namespace, not chart.targetNamespace",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "heir-system"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{TargetNamespace: "podinfo"},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.Equal(t, "heir-system", job.Namespace)
			},
		},
		{
			name: "labels identify the chart and are shared by the job and its pod template",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				want := map[string]string{
					"app.kubernetes.io/name":       "podinfo",
					"app.kubernetes.io/managed-by": "heir",
				}
				assert.Equal(t, want, job.Labels)
				assert.Equal(t, want, job.Spec.Template.Labels)
			},
		},
		{
			name: "runs as the helmchart-<name> service account",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.Equal(t, "helmchart-podinfo", job.Spec.Template.Spec.ServiceAccountName)
			},
		},
		{
			name: "container image comes from runtime.image",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Runtime: clusteragentv1alpha1.RuntimeOptions{Image: "ghcr.io/tardigradeproj/helmer:v1"},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				assert.Equal(t, "ghcr.io/tardigradeproj/helmer:v1", job.Spec.Template.Spec.Containers[0].Image)
			},
		},
		{
			name: "always runs a single, non-restarting completion",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.Equal(t, corev1.RestartPolicyNever, job.Spec.Template.Spec.RestartPolicy)
				require.NotNil(t, job.Spec.Completions)
				assert.Equal(t, int32(1), *job.Spec.Completions)
				require.NotNil(t, job.Spec.Parallelism)
				assert.Equal(t, int32(1), *job.Spec.Parallelism)
				require.NotNil(t, job.Spec.CompletionMode)
				assert.Equal(t, batchv1.NonIndexedCompletion, *job.Spec.CompletionMode)
			},
		},
		{
			name: "backOffLimit propagates from helm.backOffLimit",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Helm: clusteragentv1alpha1.HelmOptions{BackOffLimit: new(int32(3))},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.NotNil(t, job.Spec.BackoffLimit)
				assert.Equal(t, int32(3), *job.Spec.BackoffLimit)
			},
		},
		{
			name: "activeDeadlineSeconds is nil when helm.timeout is unset",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.Nil(t, job.Spec.ActiveDeadlineSeconds)
			},
		},
		{
			name: "activeDeadlineSeconds pads helm.timeout by 10s when set",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Helm: clusteragentv1alpha1.HelmOptions{Timeout: metav1.Duration{Duration: 2 * time.Minute}},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.NotNil(t, job.Spec.ActiveDeadlineSeconds)
				assert.Equal(t, int64(130), *job.Spec.ActiveDeadlineSeconds)
			},
		},
		{
			name: "bootstrap charts run on the host network and tolerate NoSchedule/NoExecute",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Runtime: clusteragentv1alpha1.RuntimeOptions{Bootstrap: true},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.True(t, job.Spec.Template.Spec.HostNetwork)
				assert.Equal(t, []corev1.Toleration{
					{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				}, job.Spec.Template.Spec.Tolerations)
			},
		},
		{
			name: "non-bootstrap charts run on the regular network with no tolerations",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				assert.False(t, job.Spec.Template.Spec.HostNetwork)
				assert.Empty(t, job.Spec.Template.Spec.Tolerations)
			},
		},
		{
			name: "securityContext propagates from runtime.securityContext",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Runtime: clusteragentv1alpha1.RuntimeOptions{
						SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: new(bool(true))},
					},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.NotNil(t, job.Spec.Template.Spec.SecurityContext)
				require.NotNil(t, job.Spec.Template.Spec.SecurityContext.RunAsNonRoot)
				assert.True(t, *job.Spec.Template.Spec.SecurityContext.RunAsNonRoot)
			},
		},
		{
			name: "HELM_VERSION env var reflects helm.version",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Helm: clusteragentv1alpha1.HelmOptions{Version: clusteragentv1alpha1.HelmVersionV3},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				assert.Contains(t, job.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{Name: "HELM_VERSION", Value: "v3"})
			},
		},
		{
			name: "values.content adds a base64-encoded HELM_VALUES env var and a decode step, for install",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart:  clusteragentv1alpha1.ChartSpec{Name: "podinfo", Repo: "https://example.com/charts"},
					Values: clusteragentv1alpha1.ValuesSpec{Content: "replicaCount: 2"},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				container := job.Spec.Template.Spec.Containers[0]

				want := base64.StdEncoding.EncodeToString([]byte("replicaCount: 2"))
				assert.Contains(t, container.Env, corev1.EnvVar{Name: helmValuesEnvVar, Value: want})

				require.Len(t, container.Command, 3)
				assert.Equal(t, "sh", container.Command[0])
				assert.Equal(t, "-c", container.Command[1])
				assert.True(t, strings.HasPrefix(container.Command[2], `echo "$HELM_VALUES" | base64 -d > `+helmValuesFile+` && `),
					"command should decode HELM_VALUES before running helm: %s", container.Command[2])
			},
		},
		{
			name: "values.content is ignored for teardown: no HELM_VALUES, no decode step",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart:  clusteragentv1alpha1.ChartSpec{TargetNamespace: "podinfo"},
					Values: clusteragentv1alpha1.ValuesSpec{Content: "replicaCount: 2"},
				},
			},
			operation: JobOperationTeardown,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				container := job.Spec.Template.Spec.Containers[0]

				assert.Len(t, container.Env, 1, "only HELM_VERSION should be set")
				assert.Equal(t, []string{"helm", "uninstall", "podinfo", "--namespace", "podinfo"}, container.Command)
			},
		},
		{
			name: "teardown with no values.content runs a plain exec command, no shell",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{TargetNamespace: "podinfo"},
				},
			},
			operation: JobOperationTeardown,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				assert.Equal(t, []string{"helm", "uninstall", "podinfo", "--namespace", "podinfo"},
					job.Spec.Template.Spec.Containers[0].Command)
			},
		},
		{
			name: "install always has more than one step, so it's wrapped in sh -c",
			chart: &clusteragentv1alpha1.HelmChart{
				ObjectMeta: metav1.ObjectMeta{Name: "podinfo", Namespace: "default"},
				Spec: clusteragentv1alpha1.HelmChartSpec{
					Chart: clusteragentv1alpha1.ChartSpec{Name: "podinfo", Repo: "https://example.com/charts", TargetNamespace: "podinfo"},
				},
			},
			operation: JobOperationInstall,
			validate: func(t *testing.T, job *batchv1.Job) {
				require.Len(t, job.Spec.Template.Spec.Containers, 1)
				command := job.Spec.Template.Spec.Containers[0].Command
				require.Len(t, command, 3)
				assert.Equal(t, "sh", command[0])
				assert.Equal(t, "-c", command[1])
				assert.Contains(t, command[2], "'helm' 'repo' 'add' 'podinfo' 'https://example.com/charts'")
				assert.Contains(t, command[2], "'helm' 'upgrade' 'podinfo' 'podinfo/podinfo'")
				assert.Contains(t, command[2], " && ")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := GenerateJob(tt.chart, tt.operation)
			require.NoError(t, err)
			require.NotNil(t, job)
			tt.validate(t, job)
		})
	}
}
