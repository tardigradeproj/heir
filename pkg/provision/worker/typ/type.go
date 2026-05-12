package typ

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
)

type NodeProfile struct {
	KubeletConfiguration     string            `json:"kubeletConfiguration"`
	KubeletExtraArgs         map[string]string `json:"KubeletExtraArgs"`
	ApiServerExternalAddress []string          `json:"apiServerExternalAddress"`
	CNIProvider              string            `json:"CNIProvider"`
}

// Save marshals the NodeProfile to JSON and writes it to path.
func (n *NodeProfile) Save(path string) error {
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("failed to marshal node profile: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write node profile to %s: %w", path, err)
	}
	return nil
}

// Load reads the JSON-encoded NodeProfile from path and assigns it to the receiver.
func (n *NodeProfile) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read node profile from %s: %w", path, err)
	}
	if err := json.Unmarshal(data, n); err != nil {
		return fmt.Errorf("failed to unmarshal node profile: %w", err)
	}
	return nil
}

type Option func(*WorkerContext)

type WorkerContext struct {
	LogLevel                   log.Level
	Token                      string
	WorkerProfileConfigMapName string `default:"worker-profile"`
	BinDir                     string `default:"/var/lib/samaritano/bin/"`

	KubeletStateDir                string `default:"/etc/samaritano/kubelet"`
	KubeletBootstrapKubeconfigPath string `default:"/etc/samaritano/kubelet/bootstrap-kubeconfig.conf"`
	KubeletKubeConfigPath          string `default:"/etc/samaritano/kubelet/config.yaml"`
	KubeletPKIPath                 string `default:"/etc/samaritano/kubelet/pki"`
	KubeletPKICaCertPath           string `default:"/etc/samaritano/pki/ca.crt"`
	KubeletExtraArgs               map[string]string
	KubeletConfigFile              string `default:"/var/lib/samaritano/kubelet/config.yaml"`
	KubeletLogFile                 string `default:"/var/log/samaritano/kubelet.log"`
	KubeletStaticPodPath           string `default:"/etc/samaritano/manifests"`

	ContainerdAddress        string        `default:"/run/samaritano/containerd.sock"`
	ContainerdState          string        `default:"/run/samaritano/containerd"`
	ContainerdRoot           string        `default:"/var/lib/samaritano/containerd"`
	ContainerdConfig         string        `default:"/etc/lib/samaritano/containerd/config.toml"`
	ContainerdLogFile        string        `json:"/var/log/samaritano/containerd.log"`
	ContainerdStartupTimeout time.Duration // default: 90s, set in NewWorkerContextWithDefaults

	ApiServerLocalAddress string `default:"https://127.0.0.1:6443"`

	ExternalAddressNodeProfileConfigmapKey      string `default:"externalAddress"`
	KubeletExtraArgsNodeProfileConfigmapKey     string `default:"kubelet.extraArgs"`
	KubeletConfigurationNodeProfileConfigmapKey string `default:"kubelet.configuration"`
	NodeProfileLocalFilePath                    string `default:"/etc/samaritano/node-profile.json"`

	CNIEnableProviderNodeProfileConfigmapKey string `default:"cni.provider"`

	CNIBinFolderPath string `default:"/opt/cni/bin"`
}

func NewWorkerContextWithDefaults() *WorkerContext {
	wc := &WorkerContext{}

	// Populate string fields from their `default` struct tags.
	t := reflect.TypeOf(*wc)
	v := reflect.ValueOf(wc).Elem()
	for i := 0; i < t.NumField(); i++ {
		if defaultVal, ok := t.Field(i).Tag.Lookup("default"); ok {
			v.Field(i).SetString(defaultVal)
		}
	}

	wc.ContainerdStartupTimeout = 90 * time.Second
	wc.LogLevel = log.GetLevel()
	return wc
}

func WithKubeletExtraArgs(t map[string]string) Option {
	return func(j *WorkerContext) {
		j.KubeletExtraArgs = t
	}
}
