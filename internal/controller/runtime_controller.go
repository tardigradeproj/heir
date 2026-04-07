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
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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
		// Generate kubeconfigs for admin, kube-controller-manager, and kube-scheduler, storing them
		// in a single Secret named <resourceName>-auth. Each kubeconfig is signed by the CA created above
		adminCert, err := pki.SignCSR(*ca,
			pki.CSR{
				CN:        "kubernetes-admin",
				O:         "system:masters",
				Hostnames: []string{},
			}, oneYearInHour)
		if err != nil {
			log.Error(err, "Failed to generate admin certificate")
			return ctrl.Result{}, err
		}
		controllerManagerCert, err := pki.SignCSR(*ca,
			pki.CSR{
				CN:        "system:kube-controller-manager",
				O:         "system:kube-controller-manager",
				Hostnames: []string{"kube-controller-manager", "127.0.0.1"},
			}, oneYearInHour)
		if err != nil {
			log.Error(err, "Failed to generate controller-manager certificate")
			return ctrl.Result{}, err
		}
		schedulerCert, err := pki.SignCSR(*ca,
			pki.CSR{
				CN:        "system:kube-scheduler",
				O:         "system:system:kube-scheduler",
				Hostnames: []string{"127.0.0.1", "kube-scheduler"},
			},
			oneYearInHour)
		if err != nil {
			log.Error(err, "Failed to generate scheduler certificate")
			return ctrl.Result{}, err
		}
		adminConf, err := generateKubeconfig("kubernetes-admin", ca.Cert, adminCert)
		if err != nil {
			log.Error(err, "Failed to generate admin kubeconfig")
			return ctrl.Result{}, err
		}
		controllerManagerConf, err := generateKubeconfig("system:kube-controller-manager", ca.Cert, controllerManagerCert)
		if err != nil {
			log.Error(err, "Failed to generate controller-manager kubeconfig")
			return ctrl.Result{}, err
		}
		schedulerConf, err := generateKubeconfig("system:kube-scheduler", ca.Cert, schedulerCert)
		if err != nil {
			log.Error(err, "Failed to generate scheduler kubeconfig")
			return ctrl.Result{}, err
		}
		authSecret, err := r.createAuthSecret(controlPlaneRuntime, map[string][]byte{
			"admin.conf":              adminConf,
			"controller-manager.conf": controllerManagerConf,
			"scheduler.conf":          schedulerConf,
		})
		if err != nil {
			log.Error(err, "Failed to define new secret resource for auth")
			return ctrl.Result{}, err
		}
		if err = r.Create(ctx, authSecret); err != nil {
			log.Error(err, "Failed to create secret resource for auth")
			return ctrl.Result{}, err
		}
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

// setupControlPlaneConfiguration is responsible for handling the reconciliation loop of the configmap that contains s6-overlay configurations.
// ExtraArgs has no priority over CRD provided by users. The configmap name is <resourceName>-config. It should contain configurations for kube-apiserver,
// kube-scheduler and kube controller manager

func (r *RuntimeReconciler) setupControlPlaneConfiguration(controlPlaneRuntime *controlplanev1alpha1.Runtime) error {

	return nil
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
	if err := ctrl.SetControllerReference(controlPlaneRuntime, pkiSecret, r.Scheme); err != nil {
		return nil, err
	}
	return pkiSecret, nil
}

func (r *RuntimeReconciler) createAuthSecret(
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
	data map[string][]byte,
) (*corev1.Secret, error) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-auth", controlPlaneRuntime.Name),
			Namespace: controlPlaneRuntime.Namespace,
		},
		Data: data,
	}
	if err := ctrl.SetControllerReference(controlPlaneRuntime, authSecret, r.Scheme); err != nil {
		return nil, err
	}
	return authSecret, nil
}

func generateKubeconfig(username string, caCert []byte, cert *pki.Certificate) ([]byte, error) {
	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters["kubernetes"] = &clientcmdapi.Cluster{
		Server:                   "https://127.0.0.1:6443",
		CertificateAuthorityData: caCert,
	}
	kubeconfig.AuthInfos[username] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cert.Cert,
		ClientKeyData:         cert.Key,
	}
	contextName := fmt.Sprintf("%s@kubernetes", username)
	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  "kubernetes",
		AuthInfo: username,
	}
	kubeconfig.CurrentContext = contextName
	return clientcmd.Write(*kubeconfig)
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
