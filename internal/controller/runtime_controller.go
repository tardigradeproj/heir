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
	"slices"
	"time"

	"github.com/tardigrade-runtime/samaritano/pkg/pki"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
)

const (
	typeAvailableRuntime = "Available"
)

// RuntimeReconciler reconciles a Runtime object
type RuntimeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=controlplane.tardigrade.runtime.io,resources=runtimes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Runtime object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
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

	// PKI - check if there's secret name <runtime-name>-pki. If not create a single secret for all certificates.
	// The process starts by creating root CA (private and public keys) and then certificates for kube-apiserver, kube controlle-manager,
	// kube scheduler and serviceaccount and place all of them under <runtime-name>-pki secret.
	pkiSecret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-pki", controlPlaneRuntime.Name), Namespace: controlPlaneRuntime.Namespace}, pkiSecret)
	if err != nil && apierrors.IsNotFound(err) {
		oneYearInHour := time.Duration(8760) * time.Hour
		// generate CA
		ca, err := pki.GenerateSelfSignedCert()
		if err != nil {
			log.Error(err, "Failed to generate self-signed ca")
			return ctrl.Result{}, err
		}
		// apiserver
		apiserverCert, err := pki.SignCSR(*ca,
			pki.CSR{
				Name:      "kubernetes",
				O:         "kubernetes",
				CN:        "kube-apiserver",
				Hostnames: setupKubeApiServerAltNames(controlPlaneRuntime.Spec.Cluster.APIServer),
			},
			oneYearInHour)
		if err != nil {
			log.Error(err, "Failed to generate kube-apiserver certificate")
			return ctrl.Result{}, err
		}
		// --service-account-key-file
		serviceAccountCert, err := pki.SignCSR(*ca,
			pki.CSR{
				Name:      "",
				O:         "",
				CN:        "service-accounts",
				Hostnames: []string{},
			},
			oneYearInHour)
		if err != nil {
			log.Error(err, "Failed to generate service account certificate")
			return ctrl.Result{}, err
		}
		pkiSecret, err := r.createPKISecret(controlPlaneRuntime,
			map[string][]byte{
				"ca.crt":        ca.Cert,
				"ca.key":        ca.Key,
				"apiserver.crt": apiserverCert.Cert,
				"apiserver.key": apiserverCert.Key,
				"sa.crt":        serviceAccountCert.Cert,
				"sa.key":        serviceAccountCert.Key,
			},
		)
		if err != nil {
			log.Error(err, "failed to define new secret resource for PKI")
			return ctrl.Result{}, err
		}
		if err = r.Create(ctx, pkiSecret); err != nil {
			log.Error(err, "Failed to create secret resource for PKI")
			return ctrl.Result{}, err
		}
		// handle kubeconfig for core components (kube controller manager, scheduler and admin). The secret is called <resourceName>-auth
	}
	if err != nil {
		log.Error(err, "Failed to get PKI secret")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&controlplanev1alpha1.Runtime{}).
		Named("runtime").
		Complete(r)
}

func (r *RuntimeReconciler) createPKISecret(
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
	data map[string][]byte,
) (*corev1.Secret, error) {
	pkiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-pki", controlPlaneRuntime.Name),
			Namespace: controlPlaneRuntime.Namespace,
		},
		Data: data,
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, controlPlaneRuntime, r.Scheme); err != nil {
		return nil, err
	}
	return pkiSecret, nil
}

func setupKubeApiServerAltNames(apiserver controlplanev1alpha1.APIServerSpec) []string {
	sans := append([]string{}, apiserver.Sans...)
	sans = append(sans, apiserver.ExternalAddress,
		"127.0.0.1",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.cluster",
		"server.kubernetes.local",
		"api-server.kubernetes.local",
	)

	return slices.Compact(sans)
}
