package bootstrap

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/types"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/certificate"
	"k8s.io/client-go/util/certificate/csr"
	"k8s.io/client-go/util/keyutil"
	"k8s.io/klog/v2"
)

func BootstrapKubeletClientConfig(ctx context.Context, wrkCtx *typ.WorkerContext, nodeName string) error {
	log := logrus.WithFields(logrus.Fields{"component": "tls-bootstrap", "node_name": nodeName})

	switch {
	// 1: Regular kubelet kubeconfig file exists.
	// The kubelet kubeconfig has been bootstrapped already.
	case fileExists(wrkCtx.KubeletKubeConfigPath):
		return nil

	// 2: Kubelet bootstrap kubeconfig file exists.
	// The kubelet kubeconfig will be bootstrapped without a join token.
	case fileExists(wrkCtx.KubeletBootstrapKubeconfigPath):
		// Nothing to do here.

	// 3: A bootstrap kubeconfig can be created (usually via a join token).
	// Bootstrap the kubelet kubeconfig via a temporary bootstrap config file.
	case wrkCtx.Token != "":
		if err := SaveBootstrapKubeconfig(wrkCtx.Token, wrkCtx.KubeletBootstrapKubeconfigPath, wrkCtx.KubeletPKIPath); err != nil {
			return fmt.Errorf("failed to save bootstrap kubeconfig: %w", err)
		}
		log.Debug("Wrote bootstrap kubeconfig file: ", wrkCtx.KubeletBootstrapKubeconfigPath)

	// 4: None of the above, bail out.
	default:
		return errors.New("neither regular nor bootstrap kubeconfig files exist and no join token given; dunno how to make kubelet authenticate to API server")
	}

	log.Info("Bootstrapping client configuration")

	if err := retry.Do(
		func() error {
			return LoadClientCert(ctx, wrkCtx, nodeName)
		},
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.Delay(1*time.Second),
		retry.OnRetry(func(attempt uint, err error) {
			log.WithError(err).WithField("attempt", attempt+1).Debug("Failed to bootstrap client configuration, retrying after backoff")
		}),
	); err != nil {
		return fmt.Errorf("failed to bootstrap client configuration: %w", err)
	}

	log.Info("Successfully bootstrapped client configuration")
	return nil
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func LoadClientCert(ctx context.Context, wrkCtx *typ.WorkerContext, nodeName string) error {
	bootstrapClientConfig, err := loadRESTClientConfig(wrkCtx.KubeletBootstrapKubeconfigPath)
	if err != nil {
		return fmt.Errorf("unable to load bootstrap kubeconfig: %v", err)
	}
	bootstrapClient, err := clientset.NewForConfig(bootstrapClientConfig)
	if err != nil {
		return fmt.Errorf("unable to create certificates signing request client: %v", err)
	}

	store, err := certificate.NewFileStore("kubelet-client", wrkCtx.KubeletPKIPath, wrkCtx.KubeletPKIPath, "", "")
	if err != nil {
		return fmt.Errorf("unable to build bootstrap cert store")
	}

	var keyData []byte
	if cert, err := store.Current(); err == nil {
		if cert.PrivateKey != nil {
			keyData, err = keyutil.MarshalPrivateKeyToPEM(cert.PrivateKey)
			if err != nil {
				keyData = nil
			}
		}
	}
	privateKeyPath := filepath.Join(wrkCtx.KubeletPKIPath, "kubelet-client.key.tmp")
	if !verifyKeyData(keyData) {
		logrus.Info("No valid private key and/or certificate found, reusing existing private key or creating a new one")
		// Note: always call LoadOrGenerateKeyFile so that private key is
		// reused on next startup if CSR request fails.
		keyData, _, err = keyutil.LoadOrGenerateKeyFile(privateKeyPath)
		if err != nil {
			return err
		}
	}
	certData, err := requestNodeCertificate(ctx, bootstrapClient, keyData, types.NodeName(nodeName))
	if err != nil {
		return err
	}
	if _, err := store.Update(certData, keyData); err != nil {
		return err
	}
	if err := os.Remove(privateKeyPath); err != nil && !os.IsNotExist(err) {
		logrus.Info("Failed cleaning up private key file", "path", privateKeyPath, "err", err)
	}

	return writeKubeconfigFromBootstrapping(bootstrapClientConfig, wrkCtx.KubeletKubeConfigPath, store.CurrentPath())
}
func verifyKeyData(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	_, err := keyutil.ParsePrivateKeyPEM(data)
	return err == nil
}
func loadRESTClientConfig(kubeconfig string) (*restclient.Config, error) {
	// Load structured kubeconfig data from the given path.
	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	loadedConfig, err := loader.Load()
	if err != nil {
		return nil, err
	}
	// Flatten the loaded data to a particular restclient.Config based on the current context.
	return clientcmd.NewNonInteractiveClientConfig(
		*loadedConfig,
		loadedConfig.CurrentContext,
		&clientcmd.ConfigOverrides{},
		loader,
	).ClientConfig()
}

func requestNodeCertificate(ctx context.Context, client clientset.Interface, privateKeyData []byte, nodeName types.NodeName) (certData []byte, err error) {
	subject := &pkix.Name{
		Organization: []string{"system:nodes"},
		CommonName:   "system:node:" + string(nodeName),
	}

	privateKey, err := keyutil.ParsePrivateKeyPEM(privateKeyData)
	if err != nil {
		return nil, fmt.Errorf("invalid private key for certificate request: %v", err)
	}
	csrData, err := certutil.MakeCSR(privateKey, subject, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to generate certificate request: %v", err)
	}
	usages := []certificatesv1.KeyUsage{
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageClientAuth,
	}
	if _, ok := privateKey.(*rsa.PrivateKey); ok {
		usages = append(usages, certificatesv1.UsageKeyEncipherment)
	}

	// The Signer interface contains the Public() method to get the public key.
	signer, ok := privateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("private key does not implement crypto.Signer")
	}

	name, err := digestedName(signer.Public(), subject, usages)
	if err != nil {
		return nil, err
	}

	reqName, reqUID, err := csr.RequestCertificateWithContext(ctx, client, csrData, name, certificatesv1.KubeAPIServerClientKubeletSignerName, nil, usages, privateKey)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 3600*time.Second)
	defer cancel()

	klog.V(2).InfoS("Waiting for client certificate to be issued")
	return csr.WaitForCertificate(ctx, client, reqName, reqUID)
}

func digestedName(publicKey interface{}, subject *pkix.Name, usages []certificatesv1.KeyUsage) (string, error) {
	hash := sha512.New512_256()

	// Here we make sure two different inputs can't write the same stream
	// to the hash. This delimiter is not in the base64.URLEncoding
	// alphabet so there is no way to have spill over collisions. Without
	// it 'CN:foo,ORG:bar' hashes to the same value as 'CN:foob,ORG:ar'
	const delimiter = '|'
	encode := base64.RawURLEncoding.EncodeToString

	write := func(data []byte) {
		hash.Write([]byte(encode(data)))
		hash.Write([]byte{delimiter})
	}

	publicKeyData, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	write(publicKeyData)

	write([]byte(subject.CommonName))
	for _, v := range subject.Organization {
		write([]byte(v))
	}
	for _, v := range usages {
		write([]byte(v))
	}

	return fmt.Sprintf("node-csr-%s", encode(hash.Sum(nil))), nil
}
func writeKubeconfigFromBootstrapping(bootstrapClientConfig *restclient.Config, kubeconfigPath, pemPath string) error {
	// Get the CA data from the bootstrap client config.
	caFile, caData := bootstrapClientConfig.CAFile, []byte{}
	if len(caFile) == 0 {
		caData = bootstrapClientConfig.CAData
	}

	// Build resulting kubeconfig.
	kubeconfigData := clientcmdapi.Config{
		// Define a cluster stanza based on the bootstrap kubeconfig.
		Clusters: map[string]*clientcmdapi.Cluster{"default-cluster": {
			Server:                   bootstrapClientConfig.Host,
			InsecureSkipTLSVerify:    bootstrapClientConfig.Insecure,
			CertificateAuthority:     caFile,
			CertificateAuthorityData: caData,
		}},
		// Define auth based on the obtained client cert.
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"default-auth": {
			ClientCertificate: pemPath,
			ClientKey:         pemPath,
		}},
		// Define a context that connects the auth info and cluster, and set it as the default
		Contexts: map[string]*clientcmdapi.Context{"default-context": {
			Cluster:   "default-cluster",
			AuthInfo:  "default-auth",
			Namespace: "default",
		}},
		CurrentContext: "default-context",
	}

	// Marshal to disk
	return clientcmd.WriteToFile(kubeconfigData, kubeconfigPath)
}

// SaveBootstrapKubeconfig decodes the base64-encoded kubeconfig, validates it,
// writes it to dst, and extracts the cluster CA certificate into kubeletPKI/ca.crt
// so kubelet can verify the API server when it rotates its own credentials.
func SaveBootstrapKubeconfig(b64Kubeconfig, dst, kubernetesPKI string) error {
	log := logrus.WithField("path", dst)
	raw, err := base64.StdEncoding.DecodeString(b64Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to decode bootstrap kubeconfig: %w", err)
	}

	cfg, err := clientcmd.Load(raw)
	if err != nil {
		return fmt.Errorf("invalid bootstrap kubeconfig: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create kubeconfig directory: %w", err)
	}
	if err := os.WriteFile(dst, raw, 0600); err != nil {
		return fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
	}
	log.Info("bootstrap kubeconfig written")

	// Extract the cluster CA from the kubeconfig and write it to the PKI directory
	// so kubelet can use it to verify the API server during certificate rotation.
	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil {
		return fmt.Errorf("bootstrap kubeconfig has no current context")
	}
	cluster := cfg.Clusters[ctx.Cluster]
	if cluster == nil || len(cluster.CertificateAuthorityData) == 0 {
		return fmt.Errorf("bootstrap kubeconfig cluster %q has no CA data", ctx.Cluster)
	}

	if err := os.MkdirAll(kubernetesPKI, 0755); err != nil {
		return fmt.Errorf("failed to create kubelet PKI directory: %w", err)
	}
	caCertPath := filepath.Join(kubernetesPKI, "ca.crt")
	if err := os.WriteFile(caCertPath, cluster.CertificateAuthorityData, 0644); err != nil {
		return fmt.Errorf("failed to write CA certificate: %w", err)
	}
	log.WithField("path", caCertPath).Info("cluster CA certificate written")

	return nil
}
