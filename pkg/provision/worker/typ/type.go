package typ

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/api/v1alpha1"
)

type NodeProfile struct {
	KubeletConfiguration string                            `json:"kubeletConfiguration"`
	KubeletExtraArgs     map[string]string                 `json:"KubeletExtraArgs"`
	ControlPlaneEndpoint v1alpha1.ControlPlaneEndpointSpec `json:"controlPlaneEndpoint"`
	CNIProvider          string                            `json:"CNIProvider"`
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
	BinDir                     string `default:"/var/lib/heir/bin/"`
	HeirRuntimeBin             string `default:"/var/lib/heir/bin/heir"`

	KubeletStateDir                string `default:"/etc/heir/kubelet"`
	KubeletBootstrapKubeconfigPath string `default:"/etc/heir/kubelet/bootstrap-kubeconfig.conf"`
	KubeletKubeConfigPath          string `default:"/etc/heir/kubelet/config.yaml"`
	KubeletPKIPath                 string `default:"/etc/heir/kubelet/pki"`
	KubeletPKICaCertPath           string `default:"/etc/heir/pki/ca.crt"`
	KubeletExtraArgs               map[string]string
	KubeletConfigFile              string `default:"/var/lib/heir/kubelet/config.yaml"`
	KubeletLogFile                 string `default:"/var/log/heir/kubelet.log"`
	KubeletStaticPodPath           string `default:"/etc/heir/manifests"`

	ContainerdAddress        string        `default:"/run/heir/containerd.sock"`
	ContainerdState          string        `default:"/run/heir/containerd"`
	ContainerdRoot           string        `default:"/var/lib/heir/containerd"`
	ContainerdConfig         string        `default:"/etc/lib/heir/containerd/config.toml"`
	ContainerdLogFile        string        `json:"/var/log/heir/containerd.log"`
	ContainerdStartupTimeout time.Duration // default: 90s, set in NewWorkerContextWithDefaults

	ApiServerLocalAddress string `default:"https://127.0.0.1:6443"`

	ControlPlaneEndpointNodeProfileConfigmapKey string `default:"control.plane.endpoint"`
	KubeletExtraArgsNodeProfileConfigmapKey     string `default:"kubelet.extraArgs"`
	KubeletConfigurationNodeProfileConfigmapKey string `default:"kubelet.configuration"`
	NodeProfileLocalFilePath                    string `default:"/etc/heir/node-profile.json"`

	CNIEnableProviderNodeProfileConfigmapKey string `default:"cni.provider"`

	CNIBinFolderPath string `default:"/opt/cni/bin"`

	ApiServerWorkerProxyServerAddress string `default:"0.0.0.0:6443"`
}

func NewWorkerContextWithDefaults() *WorkerContext {
	wc := &WorkerContext{}

	// Populate string fields from their `default` struct tags.
	t := reflect.TypeOf(*wc)
	v := reflect.ValueOf(wc).Elem()
	for i := 0; i < t.NumField(); i++ {
		defaultVal, ok := t.Field(i).Tag.Lookup("default")
		if !ok {
			continue
		}
		field := v.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString(defaultVal)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(defaultVal, 10, 64)
			if err != nil {
				log.Warnf("invalid default value %q for field %s: %v", defaultVal, t.Field(i).Name, err)
				continue
			}
			field.SetInt(n)
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
