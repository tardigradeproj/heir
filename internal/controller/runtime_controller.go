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

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	samaritanoruntime "github.com/tardigradeproj/heir/pkg/runtime"
)

const (
	typeAvailableRuntime = "Available"
)

var layout = samaritanoruntime.NewControlPlaneLayout()

// RuntimeReconciler reconciles a Runtime object
type RuntimeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RuntimeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	controlPlaneRuntime := &controlplanev1alpha1.Runtime{}
	err := r.Get(ctx, req.NamespacedName, controlPlaneRuntime)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("control plane runtime resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get runtime control plane")
		return ctrl.Result{}, err
	}
	if len(controlPlaneRuntime.Status.Conditions) == 0 {
		meta.SetStatusCondition(&controlPlaneRuntime.Status.Conditions, metav1.Condition{
			Type:    typeAvailableRuntime,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		if err := r.Status().Update(ctx, controlPlaneRuntime); err != nil {
			log.Error(err, "Failed to update runtime status")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, controlPlaneRuntime); err != nil {
			log.Error(err, "Failed to re-fetch runtime")
			return ctrl.Result{}, err
		}
	}

	// Run each reconciliation step; on any error mark the resource Degraded and requeue.
	if err := r.setupPKIAuthConfiguration(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "failed to reconcile PKI auth configuration")
		return r.setDegraded(ctx, controlPlaneRuntime, "PKIAuthSetupFailed", err.Error())
	}
	configHash, err := r.setupControlPlaneConfiguration(ctx, controlPlaneRuntime)
	if err != nil {
		log.Error(err, "failed to reconcile control plane configuration")
		return r.setDegraded(ctx, controlPlaneRuntime, "ControlPlaneConfigFailed", err.Error())
	}
	if err := r.setupDeployment(ctx, controlPlaneRuntime, configHash); err != nil {
		log.Error(err, "failed to reconcile deployment")
		return r.setDegraded(ctx, controlPlaneRuntime, "DeploymentFailed", err.Error())
	}
	if err := r.setupService(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "failed to reconcile service")
		return r.setDegraded(ctx, controlPlaneRuntime, "ServiceFailed", err.Error())
	}
	meta.SetStatusCondition(&controlPlaneRuntime.Status.Conditions, metav1.Condition{
		Type:    typeAvailableRuntime,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciled",
		Message: "All control-plane resources are in sync",
	})
	if err := r.Status().Update(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "failed to update runtime status to Available")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setDegraded marks the Runtime as Degraded and returns a non-nil error so
// controller-runtime requeues with backoff. It is a helper to keep Reconcile readable.
func (r *RuntimeReconciler) setDegraded(
	ctx context.Context,
	obj *controlplanev1alpha1.Runtime,
	reason, message string,
) (ctrl.Result, error) {
	meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
		Type:    typeAvailableRuntime,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	_ = r.Status().Update(ctx, obj) // best-effort; original error drives the requeue
	return ctrl.Result{}, fmt.Errorf("%s: %s", reason, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1alpha1.Runtime{}).
		Named("runtime").
		Complete(r)
}

// setupService reconciles the Service that exposes the control-plane pod. It always exposes
// port 6443 for the kube-apiserver and appends any AdditionalPorts defined in ServiceSpec.
// When the Service already exists only mutable fields (ports, type, labels, annotations) are
// updated; ClusterIP is preserved to avoid triggering an immutable-field error.
func (r *RuntimeReconciler) setupService(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) error {
	desired, err := samaritanoruntime.GenerateService(controlPlaneRuntime, typ.NewWorkerContextWithDefaults())
	if err != nil {
		return err
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	// Preserve ClusterIP — Kubernetes rejects updates that change it.
	desired.Spec.ClusterIP = existing.Spec.ClusterIP

	if existing.Spec.Type == desired.Spec.Type &&
		equality.Semantic.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) &&
		equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(existing.Annotations, desired.Annotations) {
		return nil
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	return r.Update(ctx, existing)
}

// setupDeployment reconciles the Deployment that runs the control-plane container.
// It creates the Deployment when absent and updates it whenever the desired state diverges
// from the live state. configHash is written to the pod annotation so that a config change
// rolls out the pods automatically.
func (r *RuntimeReconciler) setupDeployment(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
	configHash string,
) error {
	desired, err := samaritanoruntime.GenerateDeployment(controlPlaneRuntime, layout, configHash)
	if err != nil {
		return err
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepEqual(existing.Labels, desired.Labels) {
		return nil
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

// setupControlPlaneConfiguration reconciles the <resourceName>-config ConfigMap that holds the
// s6-overlay run scripts for every supervised process. It creates the ConfigMap when absent and
// updates it only when the SHA-256 hash of the newly generated data differs from the stored one.
// Returns the hex-encoded SHA-256 hash of the current ConfigMap data.
func (r *RuntimeReconciler) setupControlPlaneConfiguration(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) (string, error) {
	log := logf.FromContext(ctx)

	desired, desiredHash, err := samaritanoruntime.GenerateControlPlaneConfig(controlPlaneRuntime, layout)
	if err != nil {
		return "", err
	}

	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
			return "", err
		}
		return desiredHash, r.Create(ctx, desired)
	}
	if err != nil {
		return "", err
	}

	existingHash, err := samaritanoruntime.HashConfigData(existing.Data)
	if err != nil {
		return "", err
	}
	if existingHash == desiredHash {
		log.Info("configmap is up to date; skipping update", "configmap", existing.Name)
		return existingHash, nil
	}
	existing.Data = desired.Data
	return desiredHash, r.Update(ctx, existing)
}

// setupPKIAuthConfiguration reconciles the <resourceName>-pki-auth Secret that holds the root CA,
// all component certificates, and kubeconfigs. If the Secret already exists it is left untouched.
// When absent, a new self-signed CA is generated and used to sign all certificates and kubeconfigs
// before the Secret is created.
func (r *RuntimeReconciler) setupPKIAuthConfiguration(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-pki-auth", controlPlaneRuntime.Name),
		Namespace: controlPlaneRuntime.Namespace,
	}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	secret, err := samaritanoruntime.GeneratePKIAuthSecret(controlPlaneRuntime, layout)
	if err != nil {
		return err
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, secret, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, secret)
}
