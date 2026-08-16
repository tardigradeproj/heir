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
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/k8s"
	"github.com/tardigradeproj/heir/pkg/token"
)

const (
	typeReadyWorkerJoinToken = "Ready"

	workerJoinTokenFinalizer  = "controlplane.tardigrade.runtime.io/worker-join-token"
	workerJoinTokenClusterKey = "controlplane.tardigrade.runtime.io/jointoken-ref"
)

// WorkerJoinTokenReconciler reconciles a WorkerJoinToken object
type WorkerJoinTokenReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=workerjointokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=workerjointokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=workerjointokens/finalizers,verbs=update
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile mints a bootstrap token + kubeconfig on the tenant cluster named by
// spec.runtimeRef and publishes it as a Secret in the WorkerJoinToken's own namespace.
// See docs/workerjointoken.md for the full design: tokens are minted once per spec.generation and never
// auto-renewed; deleting the WorkerJoinToken revokes the token on the tenant cluster.
func (r *WorkerJoinTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	joinToken := &controlplanev1alpha1.WorkerJoinToken{}
	if err := r.Get(ctx, req.NamespacedName, joinToken); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !joinToken.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, joinToken, log)
	}

	if !controllerutil.ContainsFinalizer(joinToken, workerJoinTokenFinalizer) {
		controllerutil.AddFinalizer(joinToken, workerJoinTokenFinalizer)
		if err := r.Update(ctx, joinToken); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		// The Update above bumps resourceVersion, which the watch on WorkerJoinToken
		// picks up and re-queues on its own — no explicit requeue needed here.
		return ctrl.Result{}, nil
	}

	tenantClient, caData, runtimeObj, err := r.tenantClientFor(ctx, joinToken)
	if err != nil {
		return r.setDegraded(ctx, joinToken, "RuntimeNotReady", err.Error())
	}

	needsMint := joinToken.Status.ExpiresAt == nil
	if !needsMint {
		drifted, err := r.secretDrifted(ctx, joinToken)
		if err != nil {
			return ctrl.Result{}, err
		}
		needsMint = drifted
	}
	if needsMint {
		return r.mint(ctx, joinToken, tenantClient, caData, runtimeObj, log)
	}

	return r.refreshStatus(ctx, joinToken)
}

func (r *WorkerJoinTokenReconciler) secretDrifted(ctx context.Context, joinToken *controlplanev1alpha1.WorkerJoinToken) (bool, error) {
	if joinToken.Status.SecretRef == nil {
		return true, nil
	}
	secret := &corev1.Secret{}
	name := types.NamespacedName{Name: joinToken.Status.SecretRef.Name, Namespace: joinToken.Namespace}
	if err := r.Get(ctx, name, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return secretChecksum(secret.Data["jointoken"]) != joinToken.Status.SecretChecksum, nil
}

// secretChecksum returns the hex-encoded sha256 checksum of a join-token Secret's
// "jointoken" value, used to detect drift between reconciles.
func secretChecksum(kubeconfig []byte) string {
	sum := sha256.Sum256(kubeconfig)
	return hex.EncodeToString(sum[:])
}

// tenantClientFor resolves spec.runtimeRef and builds a client for the tenant cluster
// using the admin kubeconfig
func (r *WorkerJoinTokenReconciler) tenantClientFor(
	ctx context.Context,
	joinToken *controlplanev1alpha1.WorkerJoinToken,
) (kubernetes.Interface, []byte, *controlplanev1alpha1.Runtime, error) {
	runtimeObj := &controlplanev1alpha1.Runtime{}
	runtimeName := types.NamespacedName{Name: joinToken.Spec.RuntimeRef.Name, Namespace: joinToken.Namespace}
	if err := r.Get(ctx, runtimeName, runtimeObj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, fmt.Errorf("runtime %q not found", runtimeName.Name)
		}
		return nil, nil, nil, err
	}

	kubeconfigSecret := &corev1.Secret{}
	kubeconfigSecretName := types.NamespacedName{Name: fmt.Sprintf("%s-kubeconfig", runtimeObj.Name), Namespace: runtimeObj.Namespace}
	if err := r.Get(ctx, kubeconfigSecretName, kubeconfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, nil, fmt.Errorf("runtime %q admin kubeconfig secret %q not found yet", runtimeObj.Name, kubeconfigSecretName.Name)
		}
		return nil, nil, nil, err
	}
	raw, ok := kubeconfigSecret.Data[layout.Auth.ClientKubeconfig.SecretKey]
	if !ok || len(raw) == 0 {
		return nil, nil, nil, fmt.Errorf("runtime %q admin kubeconfig secret %q has no %q key", runtimeObj.Name, kubeconfigSecretName.Name, layout.Auth.ClientKubeconfig.SecretKey)
	}

	internalHost := fmt.Sprintf("https://%s.%s.svc.cluster.local:6443", runtimeObj.Name, runtimeObj.Namespace)
	tenantClient, caData, _, err := k8s.BuildClientFromBytes(raw, internalHost)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build client for runtime %q: %w", runtimeObj.Name, err)
	}
	return tenantClient, caData, runtimeObj, nil
}

// mint issues a fresh bootstrap token on the tenant cluster, publishes the resulting
// kubeconfig as the "jointoken" key of a <name>-jointoken Secret in the management
// cluster, and best-effort revokes whatever token this WorkerJoinToken previously minted.
func (r *WorkerJoinTokenReconciler) mint(
	ctx context.Context,
	joinToken *controlplanev1alpha1.WorkerJoinToken,
	tenantClient kubernetes.Interface,
	caData []byte,
	runtimeObj *controlplanev1alpha1.Runtime,
	log logr.Logger,
) (ctrl.Result, error) {
	endpoint := runtimeObj.Spec.Cluster.ControlPlaneExternalEndpoint
	apiServerAddress := fmt.Sprintf("https://%s:%d", endpoint.APIServer.Host, endpoint.APIServer.Port)
	previousTokenID := joinToken.Status.TokenID

	tok, kubeconfigBytes, err := token.IssueBootstrapToken(ctx,
		tenantClient,
		caData,
		apiServerAddress,
		joinToken.Spec.TTL.Duration,
		token.WithLabels(map[string]string{
			workerJoinTokenClusterKey: joinToken.Name,
		}),
	)
	if err != nil {
		return r.setDegraded(ctx, joinToken, "TokenIssuanceFailed", fmt.Sprintf("failed to mint bootstrap token: %v", err))
	}

	secretName := joinToken.Name + "-jointoken"
	joinSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: joinToken.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, joinSecret, func() error {
		if joinSecret.Data == nil {
			joinSecret.Data = map[string][]byte{}
		}
		joinSecret.Data["jointoken"] = kubeconfigBytes
		return ctrl.SetControllerReference(joinToken, joinSecret, r.Scheme)
	}); err != nil {
		return r.setDegraded(ctx, joinToken, "SecretWriteFailed", fmt.Sprintf("failed to write join token secret %q: %v", secretName, err))
	}

	if previousTokenID != "" {
		if err := deleteBootstrapTokenSecret(ctx, tenantClient, previousTokenID); err != nil {
			log.Error(err, "failed to revoke previous bootstrap token on tenant cluster; it will remain valid until its own expiry", "tokenID", previousTokenID)
		}
	}

	expiresAt := metav1.NewTime(metav1.Now().Add(joinToken.Spec.TTL.Duration))
	joinToken.Status.TokenID = tok.ID
	joinToken.Status.ExpiresAt = &expiresAt
	joinToken.Status.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	joinToken.Status.SecretChecksum = secretChecksum(joinSecret.Data["jointoken"])
	meta.SetStatusCondition(&joinToken.Status.Conditions, metav1.Condition{
		Type:    typeReadyWorkerJoinToken,
		Status:  metav1.ConditionTrue,
		Reason:  "TokenIssued",
		Message: fmt.Sprintf("bootstrap token issued for runtime %q, expires at %s", runtimeObj.Name, expiresAt.Format(metav1.RFC3339Micro)),
	})
	if err := r.Status().Update(ctx, joinToken); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after minting token: %w", err)
	}

	if r.Recorder != nil {
		r.Recorder.Eventf(joinToken, nil, corev1.EventTypeNormal, "TokenIssued", "IssueBootstrapToken",
			"bootstrap token %s %q issued for runtime %q, expires at %s", joinToken.Name, tok.ID, runtimeObj.Name, expiresAt.Format(metav1.RFC3339Micro))
	}

	return ctrl.Result{RequeueAfter: joinToken.Spec.TTL.Duration}, nil
}

// refreshStatus re-evaluates the Ready condition against status.expiresAt without
// minting anything new. Once expiry has passed the condition flips to False/Expired and
// stays that way/
func (r *WorkerJoinTokenReconciler) refreshStatus(ctx context.Context, joinToken *controlplanev1alpha1.WorkerJoinToken) (ctrl.Result, error) {
	now := metav1.Now()
	if joinToken.Status.ExpiresAt != nil && now.Before(joinToken.Status.ExpiresAt) {
		meta.SetStatusCondition(&joinToken.Status.Conditions, metav1.Condition{
			Type:    typeReadyWorkerJoinToken,
			Status:  metav1.ConditionTrue,
			Reason:  "TokenIssued",
			Message: fmt.Sprintf("token valid until %s", joinToken.Status.ExpiresAt.Format(metav1.RFC3339Micro)),
		})
		if err := r.Status().Update(ctx, joinToken); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: joinToken.Status.ExpiresAt.Sub(now.Time)}, nil
	}

	meta.SetStatusCondition(&joinToken.Status.Conditions, metav1.Condition{
		Type:    typeReadyWorkerJoinToken,
		Status:  metav1.ConditionFalse,
		Reason:  "Expired",
		Message: "token has expired; ttl is immutable, so delete and recreate this object to mint a new one",
	})
	if err := r.Status().Update(ctx, joinToken); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setDegraded sets the Ready condition to False and returns a non-nil error so
// controller-runtime requeues with backoff, mirroring RuntimeReconciler.setDegraded.
func (r *WorkerJoinTokenReconciler) setDegraded(
	ctx context.Context,
	joinToken *controlplanev1alpha1.WorkerJoinToken,
	reason, message string,
) (ctrl.Result, error) {
	meta.SetStatusCondition(&joinToken.Status.Conditions, metav1.Condition{
		Type:    typeReadyWorkerJoinToken,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	_ = r.Status().Update(ctx, joinToken) // best-effort; original error drives the requeue
	return ctrl.Result{}, fmt.Errorf("%s: %s", reason, message)
}

// reconcileDelete revokes the currently issued token on the tenant cluster (best-effort)
// before removing the finalizer, so deleting a WorkerJoinToken always revokes access
// rather than waiting for the token to expire on its own.
func (r *WorkerJoinTokenReconciler) reconcileDelete(
	ctx context.Context,
	joinToken *controlplanev1alpha1.WorkerJoinToken,
	log logr.Logger,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(joinToken, workerJoinTokenFinalizer) {
		return ctrl.Result{}, nil
	}

	if joinToken.Status.TokenID != "" {
		tenantClient, _, _, err := r.tenantClientFor(ctx, joinToken)
		if err != nil {
			log.Info("could not reach tenant cluster to revoke bootstrap token; removing finalizer anyway", "reason", err.Error())
		} else if err := deleteBootstrapTokenSecret(ctx, tenantClient, joinToken.Status.TokenID); err != nil {
			log.Error(err, "failed to revoke bootstrap token on tenant cluster; removing finalizer anyway", "tokenID", joinToken.Status.TokenID)
		}
	}

	controllerutil.RemoveFinalizer(joinToken, workerJoinTokenFinalizer)
	if err := r.Update(ctx, joinToken); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// deleteBootstrapTokenSecret deletes the bootstrap-token-<id> Secret from kube-system on
// the tenant cluster, treating "already gone" as success.
func deleteBootstrapTokenSecret(ctx context.Context, tenantClient kubernetes.Interface, tokenID string) error {
	name := "bootstrap-token-" + tokenID
	err := tenantClient.CoreV1().Secrets(metav1.NamespaceSystem).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete bootstrap token secret %q: %w", name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkerJoinTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1alpha1.WorkerJoinToken{}).
		Named("workerjointoken").
		Owns(&corev1.Secret{}).
		Complete(r)
}
