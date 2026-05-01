package typ

import (
	"path/filepath"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
)

type Option func(*WorkerContext)

type WorkerContext struct {
	LogLevel                       log.Level
	Token                          string
	NodeLabels                     map[string]string
	KubeletExtraArgs               map[string]string
	KubeProxyExtraArgs             map[string]string
	BinDir                         string `default:"/var/lib/samaritano/bin/"`
	KubeletStateDir                string `default:"/etc/samaritano/kubelet"`
	KubeletBootstrapKubeconfigPath string `default:"/bootstrap-kubeconfig.conf"`
	KubeletKubeConfigPath          string `default:"/config.yaml"`
	KubeletPKIPath                 string `default:"/pki"`

	ContainerdAddress        string        `default:"/run/samaritano/containerd.sock"`
	ContainerdState          string        `default:"/run/samaritano/containerd"`
	ContainerdRoot           string        `default:"/var/lib/samaritano/containerd"`
	ContainerdConfig         string        `default:"/etc/lib/samaritano/containerd/config.toml"`
	ContainerdLogFile        string        `json:"containerd-log-file"`
	ContainerdStartupTimeout time.Duration // default: 90s, set in NewWorkerContextWithDefaults
}

func NewWorkerContextWithDefaults(kubeletStateDir string) *WorkerContext {
	wc := &WorkerContext{}

	// Populate string fields from their `default` struct tags.
	t := reflect.TypeOf(*wc)
	v := reflect.ValueOf(wc).Elem()
	for i := 0; i < t.NumField(); i++ {
		if defaultVal, ok := t.Field(i).Tag.Lookup("default"); ok {
			v.Field(i).SetString(defaultVal)
		}
	}

	if kubeletStateDir != "" {
		wc.KubeletStateDir = kubeletStateDir
	}

	wc.KubeletBootstrapKubeconfigPath = filepath.Join(wc.KubeletStateDir, wc.KubeletBootstrapKubeconfigPath)
	wc.KubeletKubeConfigPath = filepath.Join(wc.KubeletStateDir, wc.KubeletKubeConfigPath)
	wc.KubeletPKIPath = filepath.Join(wc.KubeletStateDir, wc.KubeletPKIPath)

	wc.ContainerdStartupTimeout = 90 * time.Second

	return wc
}

func WithKubeletExtraArgs(t map[string]string) Option {
	return func(j *WorkerContext) {
		j.KubeletExtraArgs = t
	}
}

func WithKubeProxyExtraArgs(t map[string]string) Option {
	return func(j *WorkerContext) {
		j.KubeProxyExtraArgs = t
	}
}

func WithNodeLabels(t map[string]string) Option {
	return func(j *WorkerContext) {
		j.NodeLabels = t
	}
}
