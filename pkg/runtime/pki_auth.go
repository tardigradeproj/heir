package runtime

import (
	"fmt"
	"slices"
	"time"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/pki"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// CertificateDuration is the default lifetime for all generated certificates.
var CertificateDuration = time.Duration(8760) * time.Hour

// APIServerAltNames builds the full list of Subject Alternative Names for the
// kube-apiserver certificate, merging user-supplied SANs with the required defaults.
func APIServerAltNames(cluster controlplanev1alpha1.UpstreamCluster) []string {
	apiServer := cluster.APIServer
	controlPlaneEndpoint := cluster.ControlPlaneEndpoint
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
	)
	for _, externalAddress := range controlPlaneEndpoint.Addresses {
		if externalAddress != "" {
			sans = append(sans, externalAddress)
		}
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

	apiserverCert, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "kubernetes",
		O:         "kubernetes",
		CN:        "kube-apiserver",
		Hostnames: APIServerAltNames(runtime.Spec.UpstreamCluster),
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

	adminConf, err := generateKubeconfig(fmt.Sprintf("samaritano-%s", runtime.Name), ca.Cert, adminCert)
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
		layout.PKI.CACert.SecretKey:                 ca.Cert,
		layout.PKI.CAKey.SecretKey:                  ca.Key,
		layout.PKI.APIServerCert.SecretKey:          apiserverCert.Cert,
		layout.PKI.APIServerKey.SecretKey:           apiserverCert.Key,
		layout.PKI.ServiceAccountCert.SecretKey:     serviceAccountCert.Cert,
		layout.PKI.ServiceAccountKey.SecretKey:      serviceAccountCert.Key,
		layout.Auth.AdminConf.SecretKey:             adminConf,
		layout.Auth.ControllerManagerConf.SecretKey: controllerManagerConf,
		layout.Auth.SchedulerConf.SecretKey:         schedulerConf,
	}
	if runtime.Spec.UpstreamCluster.Network.Konnectivity.Enabled {
		konnectivityCert, err := pki.SignCSR(*ca, pki.CSR{
			CN:        "system:konnectivity-server",
			O:         "system:konnectivity-server",
			Hostnames: []string{},
		}, CertificateDuration)
		if err != nil {
			return nil, err
		}
		konnectivityConf, err := generateKubeconfig("system:konnectivity-server", ca.Cert, konnectivityCert)
		if err != nil {
			return nil, err
		}
		data[layout.Auth.KonnectivityConf.SecretKey] = konnectivityConf
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-pki-auth", runtime.Name),
			Namespace: runtime.Namespace,
		},
		Data: data,
	}, nil
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
