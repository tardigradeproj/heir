package runtime

import (
	"fmt"
	"slices"
	"time"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/pki"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CertificateDuration is the default lifetime for all generated certificates.
var CertificateDuration = time.Duration(8760) * time.Hour

// GeneratePKISecret generates a self-signed CA, signs the kube-apiserver and
// service-account certificates with it, and returns a Secret populated with all
// key/cert pairs. The caller is responsible for setting the owner reference and
// persisting the Secret.
func GeneratePKISecret(runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) (*corev1.Secret, error) {
	ca, err := pki.GenerateSelfSignedCert()
	if err != nil {
		return nil, err
	}

	apiserverCert, err := pki.SignCSR(*ca, pki.CSR{
		Name:      "kubernetes",
		O:         "kubernetes",
		CN:        "kube-apiserver",
		Hostnames: APIServerAltNames(runtime.Spec.UpstreamCluster.APIServer),
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

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-pki", runtime.Name),
			Namespace: runtime.Namespace,
		},
		Data: map[string][]byte{
			layout.PKI.CACert.SecretKey:             ca.Cert,
			layout.PKI.CAKey.SecretKey:              ca.Key,
			layout.PKI.APIServerCert.SecretKey:      apiserverCert.Cert,
			layout.PKI.APIServerKey.SecretKey:       apiserverCert.Key,
			layout.PKI.ServiceAccountCert.SecretKey: serviceAccountCert.Cert,
			layout.PKI.ServiceAccountKey.SecretKey:  serviceAccountCert.Key,
		},
	}, nil
}

// APIServerAltNames builds the full list of Subject Alternative Names for the
// kube-apiserver certificate, merging user-supplied SANs with the required defaults.
func APIServerAltNames(apiserver controlplanev1alpha1.APIServerSpec) []string {
	sans := append([]string{}, apiserver.Sans...)
	sans = append(sans,
		apiserver.ExternalAddress,
		"127.0.0.1",
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.cluster",
		"server.kubernetes.local",
		"api-server.kubernetes.local",
	)
	sans = slices.DeleteFunc(sans, func(s string) bool {
		return s == ""
	})
	slices.Sort(sans)
	return slices.Compact(sans)
}
