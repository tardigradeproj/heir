package token

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Token struct {
	ID     string
	Secret string
}

func (t Token) String() string {
	return t.ID + "." + t.Secret
}

// CreateBootstrapToken generates a bootstrap token, persists it as a Secret in
// kube-system, and returns a base64-encoded bootstrap kubeconfig.
func CreateBootstrapToken(ctx context.Context, kubeconfig, contextName string, expiry time.Duration) (string, error) {
	t, err := Generate()
	if err != nil {
		return "", err
	}

	clientConfig := buildClientConfig(kubeconfig, contextName)
	server, caData, err := extractClusterInfo(clientConfig, contextName)
	if err != nil {
		return "", err
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return "", fmt.Errorf("failed to build rest config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	secret := NewSecret(t, expiry)
	if _, err := client.CoreV1().Secrets(secret.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("failed to create bootstrap token secret: %w", err)
	}

	bootstrapKubeconfig, err := buildBootstrapKubeconfig(t, server, caData)
	if err != nil {
		return "", fmt.Errorf("failed to build bootstrap kubeconfig: %w", err)
	}

	return base64.StdEncoding.EncodeToString(bootstrapKubeconfig), nil
}

func buildClientConfig(kubeconfig, contextName string) clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}

func extractClusterInfo(clientConfig clientcmd.ClientConfig, contextName string) (server string, caData []byte, err error) {
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return "", nil, fmt.Errorf("error to read kubeconfig: %w", err)
	}

	activeContext := rawConfig.CurrentContext
	if contextName != "" {
		activeContext = contextName
	}

	ctxEntry, ok := rawConfig.Contexts[activeContext]
	if !ok {
		return "", nil, fmt.Errorf("context %q not found in kubeconfig", activeContext)
	}
	cluster, ok := rawConfig.Clusters[ctxEntry.Cluster]
	if !ok {
		return "", nil, fmt.Errorf("cluster %q not found in kubeconfig", ctxEntry.Cluster)
	}

	if cluster.Server == "" {
		return "", nil, fmt.Errorf("cluster %q has no server URL", ctxEntry.Cluster)
	}
	if len(cluster.CertificateAuthorityData) == 0 {
		return "", nil, fmt.Errorf("cluster %q has no embedded CA data", ctxEntry.Cluster)
	}

	return cluster.Server, cluster.CertificateAuthorityData, nil
}

func buildBootstrapKubeconfig(t Token, server string, caData []byte) ([]byte, error) {
	const (
		clusterName = "bootstrap"
		userName    = "tls-bootstrap-token-user"
	)

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   server,
		CertificateAuthorityData: caData,
	}
	cfg.AuthInfos[userName] = &clientcmdapi.AuthInfo{
		Token: t.String(),
	}
	cfg.Contexts[clusterName] = &clientcmdapi.Context{
		Cluster:  clusterName,
		AuthInfo: userName,
	}
	cfg.CurrentContext = clusterName

	return clientcmd.Write(*cfg)
}

func Generate() (Token, error) {
	id, err := randomHex(3)
	if err != nil {
		return Token{}, fmt.Errorf("failed to generate token id: %w", err)
	}
	secret, err := randomHex(8)
	if err != nil {
		return Token{}, fmt.Errorf("failed to generate token secret: %w", err)
	}
	return Token{ID: id, Secret: secret}, nil
}

func NewSecret(t Token, expiry time.Duration) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-token-" + t.ID,
			Namespace: metav1.NamespaceSystem,
		},
		Type: corev1.SecretTypeBootstrapToken,
		StringData: map[string]string{
			"token-id":                       t.ID,
			"token-secret":                   t.Secret,
			"expiration":                     time.Now().UTC().Add(expiry).Format(time.RFC3339),
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
