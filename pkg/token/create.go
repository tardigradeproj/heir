package token

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Token holds the generated bootstrap token ID, secret, and the SHA256 hash
// of the cluster CA certificate used when joining nodes.
type Token struct {
	ID     string // 6 lowercase hex characters  [a-z0-9]{6}
	Secret string // 16 lowercase hex characters [a-z0-9]{16}
	CAHash string // sha256:<hex-encoded digest of the cluster CA DER bytes>
}

func (t Token) String() string {
	return fmt.Sprintf("%s.%s", t.ID, t.Secret)
}

// CreateBootstrapToken generates a bootstrap token and creates the corresponding
// Secret in kube-system on the cluster reached via kubeconfig/contextName.
// It also reads the cluster CA from certificate-authority-data in the kubeconfig,
// computes its SHA256 digest, and stores it in Token.CAHash so callers can use it
// in kubeadm-style join commands (--discovery-token-ca-cert-hash sha256:<hash>).
func CreateBootstrapToken(kubeconfig, contextName string, expiry time.Duration) (Token, error) {
	t, err := Generate()
	if err != nil {
		return Token{}, err
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return Token{}, fmt.Errorf("failed to configure kubernetes client: %w", err)
	}

	// Derive the CA hash from certificate-authority-data in the kubeconfig.
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return Token{}, fmt.Errorf("reading raw kubeconfig: %w", err)
	}
	clusterName := rawConfig.Contexts[rawConfig.CurrentContext].Cluster
	caData := rawConfig.Clusters[clusterName].CertificateAuthorityData

	caHash, err := caCertHash(caData)
	if err != nil {
		return Token{}, fmt.Errorf("computing CA hash: %w", err)
	}
	t.CAHash = "sha256:" + caHash

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return Token{}, fmt.Errorf("failed to configure kubernetes client: %w", err)
	}

	secret := NewSecret(t, expiry)
	if _, err := client.CoreV1().Secrets("kube-system").Create(
		context.Background(), secret, metav1.CreateOptions{},
	); err != nil {
		return Token{}, fmt.Errorf("failed creating bootstrap token secret: %w", err)
	}
	return t, nil
}

// caCertHash returns the lowercase hex-encoded SHA256 digest of the DER bytes
// of the first certificate found in the PEM-encoded caData.
func caCertHash(caData []byte) (string, error) {
	block, _ := pem.Decode(caData)
	if block == nil {
		return "", fmt.Errorf("no PEM block found in certificate-authority-data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parsing CA certificate: %w", err)
	}
	digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(digest[:]), nil
}

// Generate produces a new random bootstrap token whose components satisfy
// the regular expression [a-z0-9]{6}\.[a-z0-9]{16}.
func Generate() (Token, error) {
	id, err := randomHex(3) // 3 bytes → 6 hex chars
	if err != nil {
		return Token{}, fmt.Errorf("generating join token id: %w", err)
	}
	secret, err := randomHex(8) // 8 bytes → 16 hex chars
	if err != nil {
		return Token{}, fmt.Errorf("generating join token secret: %w", err)
	}
	return Token{ID: id, Secret: secret}, nil
}

// NewSecret builds the bootstrap token Secret for the given token and expiry.
// The caller is responsible for creating it in the cluster.
func NewSecret(t Token, expiry time.Duration) *corev1.Secret {
	expiration := time.Now().UTC().Add(expiry).Format(time.RFC3339)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("bootstrap-token-%s", t.ID),
			Namespace: "kube-system",
		},
		Type: corev1.SecretTypeBootstrapToken,
		StringData: map[string]string{
			"token-id":                       t.ID,
			"token-secret":                   t.Secret,
			"expiration":                     expiration,
			"usage-bootstrap-authentication": "true",
			"usage-bootstrap-signing":        "true",
			"auth-extra-groups":              "system:bootstrappers:worker",
		},
	}
}
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
