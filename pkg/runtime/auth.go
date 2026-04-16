package runtime

import (
	"fmt"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/pki"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// GenerateAuthSecret signs a certificate for each control-plane component using the
// provided CA, builds their kubeconfigs, and returns a Secret populated with the
// results. The caller is responsible for setting the owner reference and persisting
// the Secret.
func GenerateAuthSecret(runtime *controlplanev1alpha1.Runtime, ca pki.Certificate, layout ControlPlaneLayout) (*corev1.Secret, error) {
	adminCert, err := pki.SignCSR(ca, pki.CSR{
		CN:        "kubernetes-admin",
		O:         "system:masters",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	controllerManagerCert, err := pki.SignCSR(ca, pki.CSR{
		CN:        "system:kube-controller-manager",
		O:         "system:kube-controller-manager",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	schedulerCert, err := pki.SignCSR(ca, pki.CSR{
		CN:        "system:kube-scheduler",
		O:         "system:kube-scheduler",
		Hostnames: []string{},
	}, CertificateDuration)
	if err != nil {
		return nil, err
	}

	adminConf, err := generateKubeconfig("kubernetes-admin", ca.Cert, adminCert)
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

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-auth", runtime.Name),
			Namespace: runtime.Namespace,
		},
		Data: map[string][]byte{
			layout.Auth.AdminConf.SecretKey:             adminConf,
			layout.Auth.ControllerManagerConf.SecretKey: controllerManagerConf,
			layout.Auth.SchedulerConf.SecretKey:         schedulerConf,
		},
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
