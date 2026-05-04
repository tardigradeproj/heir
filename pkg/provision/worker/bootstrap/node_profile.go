package bootstrap

import (
	"context"
	"fmt"
	"time"

	retry "github.com/avast/retry-go"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

// After TLS bootstrap, samaritano uses the freshly-written kubelet.conf to talk to the Kubernetes API and fetch the worker profile.
// A ConfigMap in kube-system that contains the full configuration the worker node should use. The profile contains kubelet and containerd mirrors configuration.

const (
	profileNamespace               = "kube-system"
	profileKubeletConfigurationKey = "kubelet.configuration"
)

type Profile struct {
	KubeletConfiguration []byte
}

func ReadWorkerNodeProfile(ctx context.Context, wrkCtx *typ.WorkerContext) (*Profile, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", wrkCtx.KubeletKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from %s: %w", wrkCtx.KubeletKubeConfigPath, err)
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %w", err)
	}

	var profile *Profile
	err = retry.Do(
		func() error {
			cm, err := client.CoreV1().ConfigMaps(profileNamespace).Get(ctx, wrkCtx.WorkerProfileConfigMapName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get worker profile configmap %q: %w", wrkCtx.WorkerProfileConfigMapName, err)
			}
			kubeletConfig, ok := cm.Data[profileKubeletConfigurationKey]
			if !ok {
				// The ConfigMap exists but the key is missing — retrying won't help.
				return retry.Unrecoverable(fmt.Errorf("worker profile configmap %q has no %q key", wrkCtx.WorkerProfileConfigMapName, profileKubeletConfigurationKey))
			}
			profile = &Profile{KubeletConfiguration: []byte(kubeletConfig)}
			return nil
		},
		retry.Attempts(10),
		retry.Delay(3*time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			log.WithError(err).
				WithField("attempt", n).
				WithField("kubelet.config.key", profileKubeletConfigurationKey).
				WithField("configmap.name", wrkCtx.WorkerProfileConfigMapName).
				Error("retrying worker profile reading")
		}),
		retry.Context(ctx),
	)
	return profile, err
}
