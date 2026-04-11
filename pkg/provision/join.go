package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/artifacts"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeletBin                 = "/usr/local/bin/kubelet"
	kubeletBootstrapKubeconfig = "/etc/samaritano/kubernetes/bootstrap-kubelet.conf"
	kubeletKubeconfig          = "/etc/samaritano/kubernetes/kubelet.conf"
	kubernetesPKI              = "/etc/samaritano/kubernetes/pki"
	kubeletConfigFile          = "/var/lib/samaritano/kubelet/config.yaml"
	kubeletStaticPod           = "/etc/samaritano/kubernetes/manifests"
	kubeletCertDir             = "/var/lib/samaritano/kubelet/pki"
	containerdBin              = "/usr/local/bin/containerd"
	containerdConfiguration    = "/etc/samaritano/kubernetes/config.toml"
	cniBIn                     = "/opt/cni/bin"
	cniConfiguration           = "/etc/cni/net.d"
)

func Join(ctx context.Context, token string, opts ...Option) error {
	jointCtx := &joinContext{
		token: token,
	}
	for _, opt := range opts {
		opt(jointCtx)
	}

	log.Info("extracting kubelet")
	if err := extractStreamed("worker/kubelet", kubeletBin); err != nil {
		return fmt.Errorf("failed to extract kubelet: %w", err)
	}
	log.Info("extracting containerd")
	if err := extractStreamed("worker/containerd", containerdBin); err != nil {
		return fmt.Errorf("failed to extract containerd: %w", err)
	}
	log.Info("saving bootstrap kubeconfig")
	if err := saveBootstrapKubeconfig(jointCtx.token, kubeletBootstrapKubeconfig); err != nil {
		return fmt.Errorf("failed to save bootstrap kubeconfig: %w", err)
	}
	log.Info("saving kubelet config")
	if err := saveKubeletConfig(kubeletConfigFile); err != nil {
		return fmt.Errorf("failed to save kubelet config: %w", err)
	}
	log.Info("saving containerd config")
	if err := saveContainerdConfig(containerdConfiguration); err != nil {
		return fmt.Errorf("failed to save containerd config: %w", err)
	}
	log.Info("setting up systemd units")
	if err := setupUnits(ctx, jointCtx); err != nil {
		return fmt.Errorf("failed to setup systemd units: %w", err)
	}
	log.Info("worker node provisioning complete")
	return nil
}

// saveBootstrapKubeconfig decodes the base64-encoded kubeconfig, validates it,
// writes it to dst, and extracts the cluster CA certificate into kubeletPKI/ca.crt
// so kubelet can verify the API server when it rotates its own credentials.
func saveBootstrapKubeconfig(b64Kubeconfig string, dst string) error {
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
	log.WithField("path", dst).Info("bootstrap kubeconfig written")

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

func saveKubeletConfig(dst string) error {
	log.WithField("path", dst).Info("writing kubelet config")
	content := fmt.Sprintf(`kind: KubeletConfiguration
apiVersion: kubelet.config.k8s.io/v1beta1
authentication:
  anonymous:
    enabled: false
  webhook:
    enabled: true
  x509:
    clientCAFile: "%s/ca.crt"
authorization:
  mode: Webhook
cgroupDriver: systemd
clusterDNS:
- 10.96.0.10
clusterDomain: cluster.local
containerRuntimeEndpoint: "unix:///var/run/containerd/containerd.sock"
enableServer: true
port: 10250
serverTLSBootstrap: true
logging:
  flushFrequency: 0
  options:
    json:
      infoBufferSize: "0"
    text:
      infoBufferSize: "0"
`, kubernetesPKI)

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create kubelet config directory: %w", err)
	}
	if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write kubelet config: %w", err)
	}
	return nil
}

func saveContainerdConfig(dst string) error {
	log.WithField("path", dst).Info("writing containerd config")
	content := fmt.Sprintf(`
version = 2
[plugins."io.containerd.grpc.v1.cri"]
  [plugins."io.containerd.grpc.v1.cri".containerd]
    snapshotter = "overlayfs"
    default_runtime_name = "runc"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
    runtime_type = "io.containerd.runc.v2"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc.options]
    SystemdCgroup = true
[plugins."io.containerd.grpc.v1.cri".cni]
  bin_dir = "%s"
  conf_dir = "%s"
`, cniBIn, cniConfiguration)

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create containerd config directory: %w", err)
	}
	if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write containerd config: %w", err)
	}
	return nil
}

func extractStreamed(src string, dst string) error {
	// Open the embedded file as a stream
	source, err := artifact.FS.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	// Create the destination file
	dest, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer dest.Close()
	// RAM usage remains tiny and flat.
	_, err = io.Copy(dest, source)
	return err
}
