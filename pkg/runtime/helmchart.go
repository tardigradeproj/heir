package runtime

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	helmValuesEnvVar = "HELM_VALUES"
	helmValuesFile   = "/tmp/values.yaml"
)

// JobOperation identifies which Helm operation a HelmChart Job runs.
type JobOperation string

const (
	// JobOperationInstall runs the initial `helm install` for a HelmChart.
	JobOperationInstall JobOperation = "install"
	// JobOperationTeardown runs `helm uninstall` when a HelmChart is deleted.
	JobOperationTeardown JobOperation = "teardown"
)

func JobName(chartName string, operation JobOperation) string {
	return fmt.Sprintf("helmchart-%s-%s", chartName, operation)
}

func ServiceAccountName(chartName string) string {
	return fmt.Sprintf("helmchart-%s", chartName)
}

// GenerateRBAC builds the ServiceAccount and ClusterRoleBinding that grant helmChart's Jobs
// permission to run Helm operations against the cluster.
func GenerateRBAC(helmChart *clusteragentv1alpha1.HelmChart) (*corev1.ServiceAccount, *rbacv1.ClusterRoleBinding) {
	name := ServiceAccountName(helmChart.Name)

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: helmChart.Namespace,
		},
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      serviceAccount.Name,
				Namespace: serviceAccount.Namespace,
			},
		},
	}

	return serviceAccount, clusterRoleBinding
}

// Command returns the argv-style commands helmChart's Job must run, in order, to perform
// operation.
func Command(helmChart *clusteragentv1alpha1.HelmChart, operation JobOperation) [][]string {
	chart := helmChart.Spec.Chart
	helm := helmChart.Spec.Helm
	if operation == JobOperationTeardown {
		uninstall := []string{"helm", "uninstall", helmChart.Name, "--namespace", chart.TargetNamespace}
		if helm.Timeout.Duration > 0 {
			uninstall = append(uninstall, "--timeout", helm.Timeout.Duration.String())
		}
		return [][]string{uninstall}
	}

	// chart.Name doubles as the local name the chart's repository is registered under, since
	// it only needs to be unique within the Job's pod, which never runs more than one
	// HelmChart's commands.
	chartRef := chart.Name + "/" + chart.Name

	upgrade := []string{"helm", "upgrade", helmChart.Name, chartRef, "--install", "--namespace", chart.TargetNamespace}
	if chart.CreateNamespace {
		upgrade = append(upgrade, "--create-namespace")
	}
	if chart.Version != "" {
		upgrade = append(upgrade, "--version", chart.Version)
	}
	if helm.InsecureSkipTLSVerify {
		upgrade = append(upgrade, "--insecure-skip-tls-verify")
	}
	if helm.Timeout.Duration > 0 {
		upgrade = append(upgrade, "--timeout", helm.Timeout.Duration.String())
	}
	if helm.Atomic {
		upgrade = append(upgrade, "--atomic")
	}

	// Sorted for a deterministic command
	keys := make([]string, 0, len(helmChart.Spec.Values.Set))
	for key := range helmChart.Spec.Values.Set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		upgrade = append(upgrade, "--set", fmt.Sprintf("%s=%s", key, helmChart.Spec.Values.Set[key]))
	}
	if helmChart.Spec.Values.Content != "" {
		upgrade = append(upgrade, "--values", helmValuesFile)
	}

	repoAdd := []string{"helm", "repo", "add", chart.Name, chart.Repo}
	if helm.InsecureSkipTLSVerify {
		repoAdd = append(repoAdd, "--insecure-skip-tls-verify")
	}

	return [][]string{repoAdd, upgrade}
}

func containerCommand(decode string, steps [][]string) []string {
	shellJoin := func(args []string) string {
		quoted := make([]string, len(args))
		for i, arg := range args {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		return strings.Join(quoted, " ")
	}
	if decode == "" && len(steps) == 1 {
		return steps[0]
	}

	parts := make([]string, 0, len(steps)+1)
	if decode != "" {
		parts = append(parts, decode)
	}
	for _, step := range steps {
		parts = append(parts, shellJoin(step))
	}
	return []string{"sh", "-c", strings.Join(parts, " && ")}
}

// GenerateJob builds the Job that runs Command(helmChart, operation) in the HelmChart's
// Runtime.Image to perform operation.
func GenerateJob(helmChart *clusteragentv1alpha1.HelmChart, operation JobOperation) (*batchv1.Job, error) {
	labels := map[string]string{
		"app.kubernetes.io/name":       helmChart.Name,
		"app.kubernetes.io/managed-by": "heir",
	}

	var activeDeadlineSeconds *int64
	if timeout := helmChart.Spec.Helm.Timeout.Duration; timeout > 0 {
		activeDeadlineSeconds = new(int64(timeout.Seconds() + 10))
	}

	var tolerations []corev1.Toleration
	if helmChart.Spec.Runtime.Bootstrap {
		tolerations = []corev1.Toleration{
			{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		}
	}

	env := []corev1.EnvVar{
		{Name: "HELM_VERSION", Value: string(helmChart.Spec.Helm.Version)},
	}

	var decode string
	if content := helmChart.Spec.Values.Content; content != "" && operation != JobOperationTeardown {
		env = append(env, corev1.EnvVar{
			Name:  helmValuesEnvVar,
			Value: base64.StdEncoding.EncodeToString([]byte(content)),
		})
		decode = fmt.Sprintf("echo \"$%s\" | base64 -d > %s", helmValuesEnvVar, helmValuesFile)
	}

	command := containerCommand(decode, Command(helmChart, operation))

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JobName(helmChart.Name, operation),
			Namespace: helmChart.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          helmChart.Spec.Helm.BackOffLimit,
			Completions:           new(int32(1)),
			CompletionMode:        new(batchv1.NonIndexedCompletion),
			Parallelism:           new(int32(1)),
			ActiveDeadlineSeconds: activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: ServiceAccountName(helmChart.Name),
					SecurityContext:    helmChart.Spec.Runtime.SecurityContext,
					HostNetwork:        helmChart.Spec.Runtime.Bootstrap,
					Tolerations:        tolerations,
					Containers: []corev1.Container{
						{
							Name:    "helm",
							Image:   helmChart.Spec.Runtime.Image,
							Command: command,
							Env:     env,
						},
					},
				},
			},
		},
	}, nil
}
