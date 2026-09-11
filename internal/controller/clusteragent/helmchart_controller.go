/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusteragent

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	heirruntime "github.com/tardigradeproj/heir/pkg/runtime"
)

const (
	helmChartFinalizer = "clusteragent.tardigrade.runtime.io/helmchart"
)

// HelmChartReconciler reconciles a HelmChart object
type HelmChartReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=helmcharts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=helmcharts/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *HelmChartReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	helmChart := &clusteragentv1alpha1.HelmChart{}
	if err := r.Get(ctx, req.NamespacedName, helmChart); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !helmChart.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, helmChart, log)
	}

	if !controllerutil.ContainsFinalizer(helmChart, helmChartFinalizer) {
		controllerutil.AddFinalizer(helmChart, helmChartFinalizer)
		if err := r.Update(ctx, helmChart); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		// The Update above bumps resourceVersion, which the watch on HelmChart picks up
		// and re-queues on its own — no explicit requeue needed here.
		return ctrl.Result{}, nil
	}

	// create serviceaccount
	// create clusterrole and clusterrolebinding, use clusterrole: cluster-admin
	// create job

	return ctrl.Result{}, nil
}

// reconcileDelete runs a best-effort `helm uninstall` Job before removing the finalizer, so
// deleting a HelmChart also removes the release from the upstream cluster instead of
// orphaning it. It never blocks deletion indefinitely: if the teardown Job can't be created,
// or it runs and fails, the finalizer is removed anyway and the failure is only logged.
func (r *HelmChartReconciler) reconcileDelete(
	ctx context.Context,
	helmChart *clusteragentv1alpha1.HelmChart,
	log logr.Logger,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(helmChart, helmChartFinalizer) {
		return ctrl.Result{}, nil
	}

	teardownJob := &batchv1.Job{}
	jobKey := types.NamespacedName{
		Name:      heirruntime.JobName(helmChart.Name, heirruntime.JobOperationTeardown),
		Namespace: helmChart.Spec.Chart.TargetNamespace,
	}
	err := r.Get(ctx, jobKey, teardownJob)
	switch {
	case apierrors.IsNotFound(err):
		succeeded, err := r.installJobSucceeded(ctx, helmChart)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !succeeded {
			// Nothing was ever installed, so there's no release for `helm uninstall` to remove.
			r.deleteJobRBAC(ctx, helmChart, log)
			return r.removeFinalizer(ctx, helmChart)
		}

		command := []string{"helm", "uninstall", helmChart.Name, "--namespace", helmChart.Spec.Chart.TargetNamespace}
		newJob, err := heirruntime.GenerateJob(helmChart, command, heirruntime.JobOperationTeardown)
		if err != nil {
			log.Error(err, "failed to build teardown job; removing finalizer anyway")
			r.deleteJobRBAC(ctx, helmChart, log)
			return r.removeFinalizer(ctx, helmChart)
		}
		if err := ctrl.SetControllerReference(helmChart, newJob, r.Scheme); err != nil {
			log.Error(err, "failed to set owner reference on teardown job; removing finalizer anyway")
			r.deleteJobRBAC(ctx, helmChart, log)
			return r.removeFinalizer(ctx, helmChart)
		}
		if err := r.Create(ctx, newJob); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "failed to create teardown job; removing finalizer anyway")
			r.deleteJobRBAC(ctx, helmChart, log)
			return r.removeFinalizer(ctx, helmChart)
		}
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	}

	if jobCondition(teardownJob, batchv1.JobComplete) != nil {
		r.deleteJobRBAC(ctx, helmChart, log)
		return r.removeFinalizer(ctx, helmChart)
	}
	if jobCondition(teardownJob, batchv1.JobFailed) != nil {
		log.Info("teardown job failed; removing finalizer anyway so a broken upstream cluster doesn't block deletion",
			"job", teardownJob.Name)
		r.deleteJobRBAC(ctx, helmChart, log)
		return r.removeFinalizer(ctx, helmChart)
	}
	// Still running; the watch on batchv1.Job requeues once its status changes. Its RBAC must
	// stay in place until it reaches a terminal state above, or it will lose permission to run
	// `helm uninstall` mid-flight.
	return ctrl.Result{}, nil
}

// installJobSucceeded reports whether helmChart's install Job completed successfully.
func (r *HelmChartReconciler) installJobSucceeded(ctx context.Context, helmChart *clusteragentv1alpha1.HelmChart) (bool, error) {
	installJob := &batchv1.Job{}
	key := types.NamespacedName{
		Name:      heirruntime.JobName(helmChart.Name, heirruntime.JobOperationInstall),
		Namespace: helmChart.Spec.Chart.TargetNamespace,
	}
	if err := r.Get(ctx, key, installJob); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return jobCondition(installJob, batchv1.JobComplete) != nil, nil
}

func (r *HelmChartReconciler) deleteJobRBAC(ctx context.Context, helmChart *clusteragentv1alpha1.HelmChart, log logr.Logger) {
	name := fmt.Sprintf("helmchart-%s", helmChart.Name)

	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := r.Delete(ctx, crb); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to delete clusterrolebinding", "clusterrolebinding", name)
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: helmChart.Spec.Chart.TargetNamespace},
	}
	if err := r.Delete(ctx, sa); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to delete serviceaccount", "serviceaccount", name)
	}
}

func (r *HelmChartReconciler) removeFinalizer(ctx context.Context, helmChart *clusteragentv1alpha1.HelmChart) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(helmChart, helmChartFinalizer)
	if err := r.Update(ctx, helmChart); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// jobCondition returns the True condition of type condType on job, or nil if not present.
func jobCondition(job *batchv1.Job, condType batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range job.Status.Conditions {
		if c := &job.Status.Conditions[i]; c.Type == condType && c.Status == corev1.ConditionTrue {
			return c
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HelmChartReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusteragentv1alpha1.HelmChart{}).
		Named("clusteragent-helmchart").
		Owns(&batchv1.Job{}).
		Complete(r)
}
