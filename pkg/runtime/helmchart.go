package runtime

import (
	"fmt"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobOperation identifies which Helm operation a HelmChart Job runs.
type JobOperation string

const (
	// JobOperationInstall runs the initial `helm install` for a HelmChart.
	JobOperationInstall JobOperation = "install"
	// JobOperationUpgrade runs `helm upgrade` when a HelmChart's spec or values change.
	JobOperationUpgrade JobOperation = "upgrade"
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
			Namespace: helmChart.Spec.Chart.TargetNamespace,
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

// GenerateJob builds the Job that runs command in the HelmChart's Runtime.Image to perform
// operation. When Spec.Runtime.Bootstrap is set, the pod runs on the host network and
// tolerates all NoSchedule/NoExecute taints, so bootstrap addons (CNI, kube-proxy, etc.) can
// run before the node is marked Ready.
func GenerateJob(helmChart *clusteragentv1alpha1.HelmChart, command []string, operation JobOperation) (*batchv1.Job, error) {
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

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JobName(helmChart.Name, operation),
			Namespace: helmChart.Spec.Chart.TargetNamespace,
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
							Env: []corev1.EnvVar{
								{Name: "HELM_VERSION", Value: string(helmChart.Spec.Helm.Version)},
							},
						},
					},
				},
			},
		},
	}, nil
}
