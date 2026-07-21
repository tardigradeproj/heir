package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/pki"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	// pkiAPIServerHashAnnotation tracks the hash of SAN inputs for the API server certificate.
	pkiAPIServerHashAnnotation = "controlplane.tardigrade.runtime.io/pki-apiserver-hash"
	// pkiPlaneTunnelHashAnnotation tracks the hash of SAN inputs for the plane tunnel certificate.
	pkiPlaneTunnelHashAnnotation = "controlplane.tardigrade.runtime.io/pki-planetunnel-hash"
)

// CertificateDuration is the default lifetime for all generated certificates.
var CertificateDuration = time.Duration(8760) * time.Hour

func planeTunnelAltNames(host string) []string {
	if host == "" {
		return nil
	}
	return []string{host}
}
func sansHash(cluster []string) string {
	h := sha256.Sum256([]byte(strings.Join(cluster, ",")))
	return hex.EncodeToString(h[:])
}

// RegeneratePKILeafCerts inspects the per-cert hash annotations on secret and
// re-signs only the certificates whose SAN inputs have changed since the last
// reconciliation. The CA cert and key are never replaced. Returns true if at
// least one certificate was regenerated and the secret should be updated.
func RegeneratePKILeafCerts(secret *corev1.Secret, runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) (bool, error) {
	cluster := runtime.Spec.Cluster
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	ca := pki.Certificate{
		Cert: secret.Data[layout.PKI.CACert.SecretKey],
		Key:  secret.Data[layout.PKI.CAKey.SecretKey],
	}

	updated := false
	apiServerAltNames := APIServerAltNames(*runtime)
	if wantHash := sansHash(apiServerAltNames); secret.Annotations[pkiAPIServerHashAnnotation] != wantHash {
		cert, err := pki.SignCSR(ca, pki.CSR{
			Name:      "kubernetes",
			O:         "kubernetes",
			CN:        "kube-apiserver",
			Hostnames: apiServerAltNames,
		}, CertificateDuration)
		if err != nil {
			return false, fmt.Errorf("failed to re-sign API server cert: %w", err)
		}
		secret.Data[layout.PKI.APIServerCert.SecretKey] = cert.Cert
		secret.Data[layout.PKI.APIServerKey.SecretKey] = cert.Key
		secret.Annotations[pkiAPIServerHashAnnotation] = wantHash
		updated = true
	}
	planeTunnelAltnames := planeTunnelAltNames(cluster.ControlPlaneExternalEndpoint.PlaneTunnel.Host)
	if wantHash := sansHash(planeTunnelAltnames); secret.Annotations[pkiPlaneTunnelHashAnnotation] != wantHash {
		cert, err := pki.SignCSR(ca, pki.CSR{
			Name:      "plane-tunnel",
			O:         "system:plane-tunnel",
			Hostnames: planeTunnelAltnames,
		}, CertificateDuration)
		if err != nil {
			return false, fmt.Errorf("failed to re-sign plane tunnel cert: %w", err)
		}
		secret.Data[layout.PKI.PlaneTunnelCert.SecretKey] = cert.Cert
		secret.Data[layout.PKI.PlaneTunnelKey.SecretKey] = cert.Key
		secret.Annotations[pkiPlaneTunnelHashAnnotation] = wantHash
		updated = true
	}

	return updated, nil
}

// APIServerAltNames builds the full list of Subject Alternative Names for the
// kube-apiserver certificate, merging user-supplied SANs with the required defaults.
// The in-cluster FQDN <name>.<namespace>.svc.cluster.local is always included so
// that pods inside the management cluster can reach the API server through its Service.
func APIServerAltNames(runtime controlplanev1alpha1.Runtime) []string {
	apiServer := runtime.Spec.Cluster.APIServer
	controlPlaneEndpoint := runtime.Spec.Cluster.ControlPlaneExternalEndpoint
	sans := append([]string{}, apiServer.Sans...)
	sans = append(sans,
		"127.0.0.1",
		"10.96.0.1",
		"0.0.0.0",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.cluster",
		"server.kubernetes.local",
		"api-server.kubernetes.local",
		fmt.Sprintf("%s.%s.svc.cluster.local", runtime.Name, runtime.Namespace),
	)
	if h := controlPlaneEndpoint.APIServer.Host; h != "" {
		sans = append(sans, h)
	}

	sans = slices.DeleteFunc(sans, func(s string) bool {
		return s == ""
	})
	slices.Sort(sans)
	return slices.Compact(sans)
}

// GeneratePKIAuthSecret generates a self-signed CA, signs all component certificates,
// builds kubeconfigs for each control-plane component, and returns a single Secret
// containing both PKI material and kubeconfigs. The caller is responsible for setting
// the owner reference and persisting the result.
func GeneratePKIAuthSecret(runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) (*corev1.Secret, error) {
	ca, err := pki.GenerateSelfSignedCert()
	if err != nil {
		return nil, err
	}
	planeTunnelServer, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "plane-tunnel",
		O:         "system:plane-tunnel",
		Hostnames: planeTunnelAltNames(runtime.Spec.Cluster.ControlPlaneExternalEndpoint.PlaneTunnel.Host),
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}
	apiServerPlaneTunnelServer, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "plane-tunnel",
		O:         "system:apiserver:plane-tunnel",
		Hostnames: []string{PlaneTunnelEgressName(runtime.Name)},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}
	apiServerCert, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "kubernetes",
		O:         "kubernetes",
		CN:        "kube-apiserver",
		Hostnames: APIServerAltNames(*runtime),
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	serviceAccountCert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "service-accounts",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	adminCert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "kubernetes-admin",
		O:         "system:masters",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	controllerManagerCert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "system:kube-controller-manager",
		O:         "system:kube-controller-manager",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	schedulerCert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "system:kube-scheduler",
		O:         "system:kube-scheduler",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	adminConf, err := generateKubeconfig(runtime.Name, ca.Cert, adminCert)
	if err != nil {
		return nil, err
	}
	controllerManagerConf, err := generateKubeconfig("system:kube-controller-manager", ca.Cert, controllerManagerCert)
	if err != nil {
		return nil, err
	}
	schedulerConf, err := generateKubeconfig("system:kube-scheduler", ca.Cert, schedulerCert)
	if err != nil {
		return nil, err
	}
	data := map[string][]byte{
		layout.PKI.CACert.SecretKey:                   ca.Cert,
		layout.PKI.CAKey.SecretKey:                    ca.Key,
		layout.PKI.APIServerCert.SecretKey:            apiServerCert.Cert,
		layout.PKI.APIServerKey.SecretKey:             apiServerCert.Key,
		layout.PKI.ServiceAccountCert.SecretKey:       serviceAccountCert.Cert,
		layout.PKI.ServiceAccountKey.SecretKey:        serviceAccountCert.Key,
		layout.PKI.PlaneTunnelKey.SecretKey:           planeTunnelServer.Key,
		layout.PKI.PlaneTunnelCert.SecretKey:          planeTunnelServer.Cert,
		layout.PKI.ApiServerPlaneTunnelKey.SecretKey:  apiServerPlaneTunnelServer.Key,
		layout.PKI.ApiServerPlaneTunnelCert.SecretKey: apiServerPlaneTunnelServer.Cert,
		layout.Auth.AdminConf.SecretKey:               adminConf,
		layout.Auth.ControllerManagerConf.SecretKey:   controllerManagerConf,
		layout.Auth.SchedulerConf.SecretKey:           schedulerConf,
	}
	labels := map[string]string{
		"app.kubernetes.io/name":       runtime.Name,
		"app.kubernetes.io/managed-by": "heir",
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtime.Name,
			Namespace: runtime.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"controlplane.tardigrade.runtime.io/deletion-protection": "false",
				pkiAPIServerHashAnnotation:                               sansHash(APIServerAltNames(*runtime)),
				pkiPlaneTunnelHashAnnotation:                             sansHash(planeTunnelAltNames(runtime.Spec.Cluster.ControlPlaneExternalEndpoint.PlaneTunnel.Host)),
			},
		},
		Data: data,
	}, nil
}

func generateKubeconfig(username string, caCert []byte, cert *pki.Certificate) ([]byte, error) {
	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters[username] = &clientcmdapi.Cluster{
		Server:                   "https://127.0.0.1:6443",
		CertificateAuthorityData: caCert,
	}
	kubeconfig.AuthInfos[username] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cert.Cert,
		ClientKeyData:         cert.Key,
	}
	contextName := fmt.Sprintf("%s@heir", username)
	kubeconfig.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  username,
		AuthInfo: username,
	}
	kubeconfig.CurrentContext = contextName
	return clientcmd.Write(*kubeconfig)
}
