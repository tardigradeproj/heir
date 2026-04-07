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
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
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

var layout = newControlPlaneLayout()
var certificateDurationInHour = time.Duration(8760) * time.Hour

// mountEntry binds a Secret/ConfigMap key to its absolute mount path inside the control-plane container.
type mountEntry struct {
	// SecretKey is the key name used in the Kubernetes Secret or ConfigMap.
	SecretKey string
	// MountPath is the absolute path where the file will be projected inside the container.
	MountPath string
}

// pkiLayout describes the certificate and key entries stored in the <name>-pki Secret.
type pkiLayout struct {
	CACert             mountEntry
	CAKey              mountEntry
	APIServerCert      mountEntry
	APIServerKey       mountEntry
	ServiceAccountCert mountEntry
	ServiceAccountKey  mountEntry
}

// authLayout describes the kubeconfig entries stored in the <name>-auth Secret.
type authLayout struct {
	AdminConf             mountEntry
	ControllerManagerConf mountEntry
	SchedulerConf         mountEntry
}

// configLayout describes the s6-overlay run-script entries stored in the <name>-config ConfigMap.
// Each entry is the run script for one supervised service.
type configLayout struct {
	APIServer         mountEntry
	ControllerManager mountEntry
	Scheduler         mountEntry
	Kine              mountEntry
}

// controlPlaneLayout groups all Secret/ConfigMap keys and their container mount paths for a
// control-plane instance. Use newControlPlaneLayout to obtain the canonical set of values.
type controlPlaneLayout struct {
	PKI    pkiLayout
	Auth   authLayout
	Config configLayout
}

// newControlPlaneLayout returns the fixed layout that describes every file that must be
// projected into the control-plane container: PKI certificates, kubeconfigs, and s6-overlay
// service scripts.
func newControlPlaneLayout() controlPlaneLayout {
	return controlPlaneLayout{
		PKI: pkiLayout{
			CACert:             mountEntry{SecretKey: "ca.crt", MountPath: "/etc/kubernetes/pki/ca.crt"},
			CAKey:              mountEntry{SecretKey: "ca.key", MountPath: "/etc/kubernetes/pki/ca.key"},
			APIServerCert:      mountEntry{SecretKey: "apiserver.crt", MountPath: "/etc/kubernetes/pki/kube-apiserver.crt"},
			APIServerKey:       mountEntry{SecretKey: "apiserver.key", MountPath: "/etc/kubernetes/pki/kube-apiserver.key"},
			ServiceAccountCert: mountEntry{SecretKey: "sa.crt", MountPath: "/etc/kubernetes/pki/service-accounts.crt"},
			ServiceAccountKey:  mountEntry{SecretKey: "sa.key", MountPath: "/etc/kubernetes/pki/service-accounts.key"},
		},
		Auth: authLayout{
			AdminConf:             mountEntry{SecretKey: "admin.conf", MountPath: "/etc/kubernetes/admin.conf"},
			ControllerManagerConf: mountEntry{SecretKey: "controller-manager.conf", MountPath: "/etc/kubernetes/kube-controller-manager.conf"},
			SchedulerConf:         mountEntry{SecretKey: "scheduler.conf", MountPath: "/etc/kubernetes/kube-scheduler.conf"},
		},
		Config: configLayout{
			APIServer:         mountEntry{SecretKey: "kube-apiserver", MountPath: "/etc/s6-overlay/s6-rc.d/kube-apiserver/run"},
			ControllerManager: mountEntry{SecretKey: "kube-controller-manager", MountPath: "/etc/s6-overlay/s6-rc.d/kube-controller-manager/run"},
			Scheduler:         mountEntry{SecretKey: "kube-scheduler", MountPath: "/etc/s6-overlay/s6-rc.d/kube-scheduler/run"},
			Kine:              mountEntry{SecretKey: "kine", MountPath: "/etc/s6-overlay/s6-rc.d/kine/run"},
		},
	}
}

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

	if err := r.setupPKIConfiguration(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "Failed to reconcile PKI configuration")
		return ctrl.Result{}, err
	}
	if err := r.setupAuthConfiguration(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "Failed to reconcile auth configuration")
		return ctrl.Result{}, err
	}
	if configHash, err := r.setupControlPlaneConfiguration(ctx, controlPlaneRuntime); err != nil {
		log.Error(err, "Failed to reconcile control plane configuration")
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

// setupControlPlaneConfiguration reconciles the <resourceName>-config ConfigMap that holds the
// s6-overlay run scripts for every supervised process. It creates the ConfigMap when absent and
// updates it only when the SHA-256 hash of the newly generated data differs from the stored one.
// ExtraArgs values always win over the built-in defaults; no flag is emitted twice.
// Returns the hex-encoded SHA-256 hash of the current ConfigMap data.
func (r *RuntimeReconciler) setupControlPlaneConfiguration(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) (string, error) {
	log := logf.FromContext(ctx)
	net := controlPlaneRuntime.Spec.Cluster.Network
	apiserverScript := renderRunScript("/usr/local/bin/kube-apiserver",
		mergeArgs(map[string]string{
			"allow-privileged":                 "true",
			"authorization-mode":               "Node,RBAC",
			"bind-address":                     "0.0.0.0",
			"client-ca-file":                   layout.PKI.CACert.MountPath,
			"enable-admission-plugins":         "NamespaceLifecycle,NodeRestriction,LimitRanger,ServiceAccount,DefaultStorageClass,ResourceQuota",
			"etcd-servers":                     "http://127.0.0.1:2379",
			"event-ttl":                        "1h",
			"kubelet-certificate-authority":    layout.PKI.CACert.MountPath,
			"kubelet-client-certificate":       layout.PKI.APIServerCert.MountPath,
			"kubelet-client-key":               layout.PKI.APIServerKey.MountPath,
			"runtime-config":                   "api/all=true",
			"service-account-issuer":           "https://kubernetes.default.svc.cluster.local",
			"service-account-key-file":         layout.PKI.ServiceAccountCert.MountPath,
			"service-account-signing-key-file": layout.PKI.ServiceAccountKey.MountPath,
			"service-cluster-ip-range":         net.ServiceCIDR,
			"service-node-port-range":          "30000-32767",
			"tls-cert-file":                    layout.PKI.APIServerCert.MountPath,
			"tls-private-key-file":             layout.PKI.APIServerKey.MountPath,
			"v":                                "2",
		}, controlPlaneRuntime.Spec.Cluster.APIServer.ExtraArgs),
	)

	controllerManagerScript := renderRunScript("/usr/local/bin/kube-controller-manager",
		mergeArgs(map[string]string{
			"allocate-node-cidrs":              "true",
			"bind-address":                     "0.0.0.0",
			"cluster-cidr":                     net.PodCIDR,
			"cluster-name":                     "tardigrade",
			"cluster-signing-cert-file":        layout.PKI.CACert.MountPath,
			"cluster-signing-key-file":         layout.PKI.CAKey.MountPath,
			"kubeconfig":                       layout.Auth.ControllerManagerConf.MountPath,
			"root-ca-file":                     layout.PKI.CACert.MountPath,
			"service-account-private-key-file": layout.PKI.ServiceAccountKey.MountPath,
			"service-cluster-ip-range":         net.ServiceCIDR,
			"use-service-account-credentials":  "true",
			"v":                                "2",
		}, controlPlaneRuntime.Spec.Cluster.ControllerManager.ExtraArgs),
	)

	schedulerScript := renderRunScript("/usr/local/bin/kube-scheduler",
		mergeArgs(map[string]string{
			"authentication-kubeconfig": layout.Auth.SchedulerConf.MountPath,
			"authorization-kubeconfig":  layout.Auth.SchedulerConf.MountPath,
			"bind-address":              "127.0.0.1",
			"kubeconfig":                layout.Auth.SchedulerConf.MountPath,
			"leader-elect":              "true",
		}, controlPlaneRuntime.Spec.Cluster.Scheduler.ExtraArgs),
	)

	kineArgs := map[string]string{}
	if controlPlaneRuntime.Spec.Cluster.Storage.Kine.DataSource != "" {
		kineArgs["endpoint"] = controlPlaneRuntime.Spec.Cluster.Storage.Kine.DataSource
	}
	kineScript := renderRunScript("/usr/local/bin/kine", kineArgs)

	desired := map[string]string{
		layout.Config.APIServer.SecretKey:         apiserverScript,
		layout.Config.ControllerManager.SecretKey: controllerManagerScript,
		layout.Config.Scheduler.SecretKey:         schedulerScript,
		layout.Config.Kine.SecretKey:              kineScript,
	}

	desiredHash, err := hashConfigData(desired)
	if err != nil {
		return "", err
	}

	configMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-config", controlPlaneRuntime.Name),
		Namespace: controlPlaneRuntime.Namespace,
	}, configMap)
	if err != nil && apierrors.IsNotFound(err) {
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-config", controlPlaneRuntime.Name),
				Namespace: controlPlaneRuntime.Namespace,
			},
			Data: desired,
		}
		if err := ctrl.SetControllerReference(controlPlaneRuntime, configMap, r.Scheme); err != nil {
			return "", err
		}
		return desiredHash, r.Create(ctx, configMap)
	}
	if err != nil {
		return "", err
	}
	existingHash, err := hashConfigData(configMap.Data)
	if err != nil {
		return "", err
	}
	if existingHash == desiredHash {
		log.Info("configmap-config got updated configuration data", "configmap", configMap.Name)
		return existingHash, nil
	}
	configMap.Data = desired
	return desiredHash, r.Update(ctx, configMap)
}

// hashConfigData returns the hex-encoded SHA-256 digest of a ConfigMap data map.
// encoding/json marshals map keys in sorted order, ensuring a deterministic digest.
func hashConfigData(data map[string]string) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

// mergeArgs returns a new map with all entries from defaults overridden by any matching key in extra.
func mergeArgs(defaults, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(extra))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// renderRunScript produces an s6-overlay execlineb run script for the given binary and args.
// Args are emitted in sorted order for deterministic output.
func renderRunScript(binary string, args map[string]string) string {
	var sb strings.Builder
	sb.WriteString("#!/command/execlineb -P\n")
	sb.WriteString("fdmove -c 2 1\n")
	if len(args) == 0 {
		sb.WriteString(binary)
		return sb.String()
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sb.WriteString(binary + " \\\n")
	for i, k := range keys {
		if i < len(keys)-1 {
			fmt.Fprintf(&sb, "  --%s=%s \\\n", k, args[k])
		} else {
			fmt.Fprintf(&sb, "  --%s=%s", k, args[k])
		}
	}
	return sb.String()
}

// setupPKIConfiguration reconciles the <resourceName>-pki Secret that holds the root CA and all
// component certificates. If the Secret already exists it is left untouched. When absent, a new
// self-signed CA is generated and used to sign the kube-apiserver and service-account certificates
// before the Secret is created.
func (r *RuntimeReconciler) setupPKIConfiguration(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) error {
	pkiSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-pki", controlPlaneRuntime.Name),
		Namespace: controlPlaneRuntime.Namespace,
	}, pkiSecret)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	ca, err := pki.GenerateSelfSignedCert()
	if err != nil {
		return err
	}
	apiserverCert, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "kubernetes",
		O:         "kubernetes",
		CN:        "kube-apiserver",
		Hostnames: setupKubeApiServerAltNames(controlPlaneRuntime.Spec.Cluster.APIServer),
	}, certificateDurationInHour)
	if err != nil {
		return err
	}
	serviceAccountCert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "service-accounts",
		Hostnames: []string{},
	}, certificateDurationInHour)
	if err != nil {
		return err
	}

	secret, err := r.createPKISecret(controlPlaneRuntime, map[string][]byte{
		layout.PKI.CACert.SecretKey:             ca.Cert,
		layout.PKI.CAKey.SecretKey:              ca.Key,
		layout.PKI.APIServerCert.SecretKey:      apiserverCert.Cert,
		layout.PKI.APIServerKey.SecretKey:       apiserverCert.Key,
		layout.PKI.ServiceAccountCert.SecretKey: serviceAccountCert.Cert,
		layout.PKI.ServiceAccountKey.SecretKey:  serviceAccountCert.Key,
	})
	if err != nil {
		return err
	}
	return r.Create(ctx, secret)
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

// setupAuthConfiguration reconciles the <resourceName>-auth Secret that holds kubeconfigs for
// admin, kube-controller-manager, and kube-scheduler. If the Secret already exists it is left
// untouched — certificates are long-lived and should not be rotated on every loop. When absent,
// the CA is read from the <resourceName>-pki Secret and used to sign a fresh certificate for
// each component before the Secret is created.
func (r *RuntimeReconciler) setupAuthConfiguration(
	ctx context.Context,
	controlPlaneRuntime *controlplanev1alpha1.Runtime,
) error {
	log := logf.FromContext(ctx)
	authSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-auth", controlPlaneRuntime.Name),
		Namespace: controlPlaneRuntime.Namespace,
	}, authSecret)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		log.Error(err, "failed to get retrieve auth secret", "name", controlPlaneRuntime.Name)
		return err
	}

	// Auth secret is absent — read the CA from the PKI secret.
	pkiSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      fmt.Sprintf("%s-pki", controlPlaneRuntime.Name),
		Namespace: controlPlaneRuntime.Namespace,
	}, pkiSecret); err != nil {
		return err
	}
	ca := pki.Certificate{
		Cert: pkiSecret.Data[layout.PKI.CACert.SecretKey],
		Key:  pkiSecret.Data[layout.PKI.CAKey.SecretKey],
	}

	adminCert, err := pki.SignCSR(ca, pki.CSR{CN: "kubernetes-admin", O: "system:masters", Hostnames: []string{}}, certificateDurationInHour)
	if err != nil {
		return err
	}
	controllerManagerCert, err := pki.SignCSR(ca, pki.CSR{CN: "system:kube-controller-manager", O: "system:kube-controller-manager", Hostnames: []string{}}, certificateDurationInHour)
	if err != nil {
		return err
	}
	schedulerCert, err := pki.SignCSR(ca, pki.CSR{CN: "system:kube-scheduler", O: "system:kube-scheduler", Hostnames: []string{}}, certificateDurationInHour)
	if err != nil {
		return err
	}

	adminConf, err := generateKubeconfig("kubernetes-admin", ca.Cert, adminCert)
	if err != nil {
		return err
	}
	controllerManagerConf, err := generateKubeconfig("system:kube-controller-manager", ca.Cert, controllerManagerCert)
	if err != nil {
		return err
	}
	schedulerConf, err := generateKubeconfig("system:kube-scheduler", ca.Cert, schedulerCert)
	if err != nil {
		return err
	}

	secret, err := r.createAuthSecret(controlPlaneRuntime, map[string][]byte{
		layout.Auth.AdminConf.SecretKey:             adminConf,
		layout.Auth.ControllerManagerConf.SecretKey: controllerManagerConf,
		layout.Auth.SchedulerConf.SecretKey:         schedulerConf,
	})
	if err != nil {
		return err
	}
	return r.Create(ctx, secret)
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
