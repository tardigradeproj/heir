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

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	heirruntime "github.com/tardigradeproj/heir/pkg/runtime"
)

const (
	typeAvailableRuntime = "Available"
)

var layout = heirruntime.NewControlPlaneLayout()

// RuntimeReconciler reconciles a Runtime object
type RuntimeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

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
	if err := r.setupPKIAuthConfiguration(ctx, controlPlaneRuntime, log); err != nil {
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
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
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
	desired, err := heirruntime.GenerateService(controlPlaneRuntime)
	if err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "ServiceGenerationFailed", "GenerateService",
			"failed to generate service spec: %v", err)
		return err
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "ServiceCreateFailed", "CreateService",
				"failed to create service %q: %v", desired.Name, err)
			return err
		}
		r.Recorder.Eventf(controlPlaneRuntime, desired, corev1.EventTypeNormal, "ServiceCreated", "CreateService",
			"service %q created", desired.Name)
		return nil
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
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "ServiceUpdateFailed", "UpdateService",
			"failed to update service %q: %v", existing.Name, err)
		return err
	}
	r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeNormal, "ServiceUpdated", "UpdateService",
		"service %q updated", existing.Name)
	return nil
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
	desired, err := heirruntime.GenerateDeployment(controlPlaneRuntime, layout, configHash)
	if err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "DeploymentGenerationFailed", "GenerateDeployment",
			"failed to generate deployment spec: %v", err)
		return err
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "DeploymentCreateFailed", "CreateDeployment",
				"failed to create deployment %q: %v", desired.Name, err)
			return err
		}
		r.Recorder.Eventf(controlPlaneRuntime, desired, corev1.EventTypeNormal, "DeploymentCreated", "CreateDeployment",
			"deployment %q created", desired.Name)
		return nil
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
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "DeploymentUpdateFailed", "UpdateDeployment",
			"failed to update deployment %q: %v", existing.Name, err)
		return err
	}
	r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeNormal, "DeploymentUpdated", "UpdateDeployment",
		"deployment %q updated (config hash: %s)", existing.Name, configHash[:8])
	return nil
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

	desired, desiredHash, err := heirruntime.GenerateControlPlaneConfig(controlPlaneRuntime, layout)
	if err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "ConfigGenerationFailed", "GenerateConfig",
			"failed to generate control plane configuration: %v", err)
		return "", err
	}

	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if err != nil && apierrors.IsNotFound(err) {
		if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
			return "", err
		}
		if err := r.Create(ctx, desired); err != nil {
			r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "ConfigMapCreateFailed", "CreateConfigMap",
				"failed to create config map %q: %v", desired.Name, err)
			return "", err
		}
		r.Recorder.Eventf(controlPlaneRuntime, desired, corev1.EventTypeNormal, "ConfigMapCreated", "CreateConfigMap",
			"config map %q created", desired.Name)
		return desiredHash, nil
	}
	if err != nil {
		return "", err
	}
	if !metav1.IsControlledBy(existing, controlPlaneRuntime) {
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "ConfigMapExists", "ConfigMapOwnershipValidation",
			"config map %q already exists but is not owned by Heir Runtime", desired.Name)
		return "", fmt.Errorf("secret %s/%s exists but is not owned by Heir Runtime; refusing to adopt", existing.Namespace, existing.Name)
	}
	existingHash, err := heirruntime.HashConfigData(existing.Data)
	if err != nil {
		return "", err
	}
	if existingHash == desiredHash {
		log.Info("configmap is up to date; skipping update", "configmap", existing.Name)
		return existingHash, nil
	}
	existing.Data = desired.Data
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "ConfigMapUpdateFailed", "UpdateConfigMap",
			"failed to update config map %q: %v", existing.Name, err)
		return "", err
	}
	r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeNormal, "ConfigMapUpdated", "UpdateConfigMap",
		"config map %q updated (config hash changed from %s to %s)", existing.Name, existingHash[:8], desiredHash[:8])
	return desiredHash, nil
}

// setupPKIAuthConfiguration reconciles the Secret that holds the root CA,
// all component certificates, and kubeconfigs.
//
// On first creation a fresh self-signed CA is generated and all certs are
// signed against it. On subsequent reconciliations only the leaf certs whose
// SANs are driven by the spec (API server cert, plane tunnel cert) are
// regenerated when the spec changes; the CA is always preserved so that
// existing clients do not need to re-trust the root.
func (r *RuntimeReconciler) setupPKIAuthConfiguration(
	ctx context.Context,
	runtime *controlplanev1alpha1.Runtime,
	log logr.Logger,
) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      runtime.Name,
		Namespace: runtime.Namespace,
	}, existing)

	if apierrors.IsNotFound(err) {
		secret, err := heirruntime.GeneratePKIAuthSecret(runtime, layout)
		if err != nil {
			r.Recorder.Eventf(runtime, nil, corev1.EventTypeWarning, "PKIGenerationFailed", "GeneratePKI",
				"failed to generate PKI material: %v", err)
			return err
		}
		if err := ctrl.SetControllerReference(runtime, secret, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, secret); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			r.Recorder.Eventf(runtime, nil, corev1.EventTypeWarning, "PKISecretCreateFailed", "CreatePKISecret",
				"failed to create PKI secret %q: %v", secret.Name, err)
			return err
		}
		r.Recorder.Eventf(runtime, secret, corev1.EventTypeNormal, "PKISecretCreated", "CreatePKISecret",
			"PKI secret %q created with CA and all component certificates", secret.Name)
		return nil
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(existing, runtime) {
		r.Recorder.Eventf(runtime, existing, corev1.EventTypeWarning, "PKISecret", "PKISecretOwnership",
			"secret is not owned by Heir Runtime: %v", err)
		return fmt.Errorf("secret %s/%s exists but is not owned by Heir Runtime; refusing to adopt", existing.Namespace, existing.Name)
	}
	updated, err := heirruntime.RegeneratePKILeafCerts(existing, runtime, layout)
	if err != nil {
		r.Recorder.Eventf(runtime, existing, corev1.EventTypeWarning, "PKICertRegenerationFailed", "RotatePKICerts",
			"failed to regenerate APIServer and/or Plane Tunnel: %v", err)
		return fmt.Errorf("failed to regenerate PKI leaf certs: %w", err)
	}
	if !updated {
		log.Info("PKI auth configuration is up to date; skipping update")
		return nil
	}
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(runtime, existing, corev1.EventTypeWarning, "PKISecretUpdateFailed", "RotatePKICerts",
			"failed to persist rotated certs in secret: %v", err)
		return err
	}
	r.Recorder.Eventf(runtime, existing, corev1.EventTypeNormal, "PKICertRotated", "RotatePKICerts",
		"APIServer and/or Plane Tunnel certificates rotated due to SAN change")
	return nil
}

// setupPlaneTunnelService reconciles the three Services required by the plane tunnel server:
// the external tunnel service (NodePort/LoadBalancer), a headless service for replica
// discovery, and a ClusterIP egress-selector service for the API server.
func (r *RuntimeReconciler) setupPlaneTunnelService(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
	wrkCtx *typ.WorkerContext,
) error {
	desired, err := heirruntime.GeneratePlaneTunnelService(*wrkCtx, controlPlaneRuntime)
	if err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "PlaneTunnelServiceGenerationFailed", "GeneratePlaneTunnelService",
			"failed to generate plane tunnel service specs: %v", err)
		return err
	}

	for i := range desired {
		svc := &desired[i]
		if err := ctrl.SetControllerReference(controlPlaneRuntime, svc, r.Scheme); err != nil {
			return err
		}
		existing := &corev1.Service{}
		err := r.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}, existing)
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, svc); err != nil {
				r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "PlaneTunnelServiceCreateFailed",
					"CreatePlaneTunnelService",
					"failed to create plane tunnel service %q: %v", svc.Name, err)
				return err
			}
			r.Recorder.Eventf(controlPlaneRuntime, svc, corev1.EventTypeNormal, "PlaneTunnelServiceCreated", "CreatePlaneTunnelService",
				"plane tunnel service %q created", svc.Name)
			continue
		}
		if err != nil {
			return err
		}
		if !metav1.IsControlledBy(existing, controlPlaneRuntime) {
			r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "PlaneTunnelServiceOwnership",
				"PlaneTunnelServiceOwnershipFailed",
				"plane tunnel service is not owned by Heir Runtime: %v", err)
			return fmt.Errorf("plane tunnel service %s/%s exists but is not owned by Heir Runtime; refusing to adopt", existing.Namespace, existing.Name)
		}
		// Preserve ClusterIP — Kubernetes rejects updates that change it.
		svc.Spec.ClusterIP = existing.Spec.ClusterIP
		if existing.Spec.Type == svc.Spec.Type &&
			equality.Semantic.DeepEqual(existing.Spec.Ports, svc.Spec.Ports) &&
			equality.Semantic.DeepEqual(existing.Labels, svc.Labels) &&
			equality.Semantic.DeepEqual(existing.Annotations, svc.Annotations) {
			continue
		}
		existing.Spec = svc.Spec
		existing.Labels = svc.Labels
		existing.Annotations = svc.Annotations
		if err := r.Update(ctx, existing); err != nil {
			r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "PlaneTunnelServiceUpdateFailed", "UpdatePlaneTunnelService",
				"failed to update plane tunnel service %q: %v", existing.Name, err)
			return err
		}
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeNormal, "PlaneTunnelServiceUpdated", "UpdatePlaneTunnelService",
			"plane tunnel service %q updated", existing.Name)
	}
	return nil
}

// setupPlaneTunnelDeployment reconciles the Deployment that runs the plane tunnel server.
func (r *RuntimeReconciler) setupPlaneTunnelDeployment(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) error {
	wrkCtx := typ.NewWorkerContextWithDefaults()
	desired := heirruntime.GeneratePlaneTunnelDeployment(*wrkCtx, controlPlaneRuntime, layout)
	if err := ctrl.SetControllerReference(controlPlaneRuntime, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			r.Recorder.Eventf(controlPlaneRuntime, nil, corev1.EventTypeWarning, "PlaneTunnelDeploymentCreateFailed", "CreatePlaneTunnelDeployment",
				"failed to create plane tunnel deployment %q: %v", desired.Name, err)
			return err
		}
		r.Recorder.Eventf(controlPlaneRuntime, desired, corev1.EventTypeNormal, "PlaneTunnelDeploymentCreated", "CreatePlaneTunnelDeployment",
			"plane tunnel deployment %q created", desired.Name)
		return nil
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
	if err := r.Update(ctx, existing); err != nil {
		r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeWarning, "PlaneTunnelDeploymentUpdateFailed", "UpdatePlaneTunnelDeployment",
			"failed to update plane tunnel deployment %q: %v", existing.Name, err)
		return err
	}
	r.Recorder.Eventf(controlPlaneRuntime, existing, corev1.EventTypeNormal, "PlaneTunnelDeploymentUpdated", "UpdatePlaneTunnelDeployment",
		"plane tunnel deployment %q updated", existing.Name)
	return nil
}
