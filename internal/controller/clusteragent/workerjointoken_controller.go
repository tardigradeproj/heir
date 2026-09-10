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
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
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

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/token"
)

const (
	typeReadyWorkerJoinToken = "Ready"

	workerJoinTokenFinalizer  = "clusteragent.tardigrade.runtime.io/worker-join-token"
	workerJoinTokenClusterKey = "clusteragent.tardigrade.runtime.io/jointoken-ref"
)

// WorkerJoinTokenReconciler reconciles a WorkerJoinToken object
type WorkerJoinTokenReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// CA is this cluster's CA certificate, used to build the bootstrap kubeconfig.
	CA []byte
	// Runtime describes this cluster, used to resolve its external API server address.
	Runtime   *controlplanev1alpha1.Runtime
	Clientset kubernetes.Interface
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=workerjointokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=workerjointokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=clusteragent.tardigrade.runtime.io,resources=workerjointokens/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile mints a bootstrap token + kubeconfig on this cluster and publishes it as a
// Secret in kube-system (WorkerJoinToken is cluster-scoped, so there is no natural
// namespace of its own to use).
func (r *WorkerJoinTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	joinToken := &clusteragentv1alpha1.WorkerJoinToken{}
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

	needsMint := joinToken.Status.ExpiresAt == nil
	if !needsMint {
		drifted, err := r.secretDrifted(ctx, joinToken)
		if err != nil {
			return ctrl.Result{}, err
		}
		needsMint = drifted
	}
	if needsMint {
		return r.mint(ctx, joinToken, log)
	}
	now := metav1.Now()
	if now.Before(joinToken.Status.ExpiresAt) {
		return ctrl.Result{RequeueAfter: joinToken.Status.ExpiresAt.Sub(now.Time)}, nil
	}
	return r.markExpired(ctx, joinToken)
}

func (r *WorkerJoinTokenReconciler) secretDrifted(ctx context.Context, joinToken *clusteragentv1alpha1.WorkerJoinToken) (bool, error) {
	if joinToken.Status.SecretRef == nil {
		return true, nil
	}
	secret := &corev1.Secret{}
	name := types.NamespacedName{Name: joinToken.Status.SecretRef.Name, Namespace: metav1.NamespaceSystem}
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

// mint issues a fresh bootstrap token on this cluster, publishes the resulting kubeconfig
// as the "jointoken" key of a <name>-jointoken Secret, and best-effort revokes whatever
// token this WorkerJoinToken previously minted.
func (r *WorkerJoinTokenReconciler) mint(
	ctx context.Context,
	joinToken *clusteragentv1alpha1.WorkerJoinToken,
	log logr.Logger,
) (ctrl.Result, error) {
	endpoint := r.Runtime.Spec.Cluster.ControlPlaneExternalEndpoint
	apiServerAddress := fmt.Sprintf("https://%s:%d", endpoint.APIServer.Host, endpoint.APIServer.Port)

	previousTokenID := joinToken.Status.TokenID
	tok, kubeconfigBytes, err := token.IssueBootstrapToken(ctx,
		r.Clientset,
		r.CA,
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
	joinSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: metav1.NamespaceSystem}}
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
		if err := deleteBootstrapTokenSecret(ctx, r.Clientset, previousTokenID); err != nil {
			log.Error(err, "failed to revoke previous bootstrap token; it will remain valid until its own expiry", "tokenID", previousTokenID)
		}
	}

	now := metav1.Now()
	expiresAt := metav1.NewTime(now.Add(joinToken.Spec.TTL.Duration))
	joinToken.Status.TokenID = tok.ID
	joinToken.Status.ExpiresAt = &expiresAt
	joinToken.Status.SecretRef = &corev1.LocalObjectReference{Name: secretName}
	joinToken.Status.SecretChecksum = secretChecksum(joinSecret.Data["jointoken"])
	meta.SetStatusCondition(&joinToken.Status.Conditions, metav1.Condition{
		Type:    typeReadyWorkerJoinToken,
		Status:  metav1.ConditionTrue,
		Reason:  "TokenIssued",
		Message: fmt.Sprintf("bootstrap token issued, expires at %s", expiresAt.Format(metav1.RFC3339Micro)),
	})
	if err := r.Status().Update(ctx, joinToken); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status after minting token: %w", err)
	}

	if r.Recorder != nil {
		r.Recorder.Eventf(joinToken, nil, corev1.EventTypeNormal, "TokenIssued", "IssueBootstrapToken",
			"bootstrap token %s %q issued, expires at %s", joinToken.Name, tok.ID, expiresAt.Format(metav1.RFC3339Micro))
	}

	return ctrl.Result{RequeueAfter: joinToken.Status.ExpiresAt.Sub(now.Time)}, nil
}

// markExpired flips the Ready condition to False/Expired once status.expiresAt has
// passed. It is a no-op (no Status write) if that condition is already recorded, so
// reconciles triggered after expiry don't churn resourceVersion forever.
func (r *WorkerJoinTokenReconciler) markExpired(ctx context.Context, joinToken *clusteragentv1alpha1.WorkerJoinToken) (ctrl.Result, error) {
	if cond := meta.FindStatusCondition(joinToken.Status.Conditions, typeReadyWorkerJoinToken); cond != nil &&
		cond.Status == metav1.ConditionFalse && cond.Reason == "Expired" {
		return ctrl.Result{}, nil
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
	joinToken *clusteragentv1alpha1.WorkerJoinToken,
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

// reconcileDelete revokes the currently issued token (best-effort) before removing the
// finalizer, so deleting a WorkerJoinToken always revokes access rather than waiting for
// the token to expire on its own.
func (r *WorkerJoinTokenReconciler) reconcileDelete(
	ctx context.Context,
	joinToken *clusteragentv1alpha1.WorkerJoinToken,
	log logr.Logger,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(joinToken, workerJoinTokenFinalizer) {
		return ctrl.Result{}, nil
	}

	if joinToken.Status.TokenID != "" {
		if err := deleteBootstrapTokenSecret(ctx, r.Clientset, joinToken.Status.TokenID); err != nil {
			log.Error(err, "failed to revoke bootstrap token; removing finalizer anyway", "tokenID", joinToken.Status.TokenID)
		}
	}

	controllerutil.RemoveFinalizer(joinToken, workerJoinTokenFinalizer)
	if err := r.Update(ctx, joinToken); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// deleteBootstrapTokenSecret deletes the bootstrap-token-<id> Secret from kube-system,
// treating "already gone" as success.
func deleteBootstrapTokenSecret(ctx context.Context, clientset kubernetes.Interface, tokenID string) error {
	name := "bootstrap-token-" + tokenID
	err := clientset.CoreV1().Secrets(metav1.NamespaceSystem).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete bootstrap token secret %q: %w", name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkerJoinTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusteragentv1alpha1.WorkerJoinToken{}).
		Named("clusteragent-workerjointoken").
		Owns(&corev1.Secret{}).
		Complete(r)
}
