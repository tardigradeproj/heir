package typ

import (
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
)

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
	KubeletExtraArgs               map[string]string
	KubeletConfigFile              string `default:"/var/lib/samaritano/kubelet/config.yaml"`
	KubeletLogFile                 string `default:"/var/log/samaritano/kubelet.log"`

	ContainerdAddress        string        `default:"/run/samaritano/containerd.sock"`
	ContainerdState          string        `default:"/run/samaritano/containerd"`
	ContainerdRoot           string        `default:"/var/lib/samaritano/containerd"`
	ContainerdConfig         string        `default:"/etc/lib/samaritano/containerd/config.toml"`
	ContainerdLogFile        string        `json:"/var/log/samaritano/containerd.log"`
	ContainerdStartupTimeout time.Duration // default: 90s, set in NewWorkerContextWithDefaults
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

	return wc
}

func WithKubeletExtraArgs(t map[string]string) Option {
	return func(j *WorkerContext) {
		j.KubeletExtraArgs = t
	}
}
