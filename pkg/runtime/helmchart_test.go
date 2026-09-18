package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func helmChart(name, targetNamespace string) *clusteragentv1alpha1.HelmChart {
	return &clusteragentv1alpha1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: clusteragentv1alpha1.HelmChartSpec{
			Chart: clusteragentv1alpha1.ChartSpec{
				TargetNamespace: targetNamespace,
			},
		},
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
			name:  "service account namespace matches chart.targetNamespace",
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
			name:  "chart name and target namespace can differ, and each object follows its own field",
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
