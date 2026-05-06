package bootstrap

import (
	"context"
	"encoding/json"
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
	profileNamespace = "kube-system"
)

func ReadWorkerNodeProfile(ctx context.Context, wrkCtx *typ.WorkerContext) (*typ.NodeProfile, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", wrkCtx.KubeletKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from %s: %w", wrkCtx.KubeletKubeConfigPath, err)
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client: %w", err)
	}
	profileKubeletConfigurationKey := wrkCtx.KubeletConfigurationNodeProfileConfigmapKey
	var profile *typ.NodeProfile
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
			kubeletExtraArgs, ok := cm.Data[wrkCtx.KubeletExtraArgsNodeProfileConfigmapKey]
			if !ok {
				return retry.Unrecoverable(fmt.Errorf("worker profile configmap %q has no %q key", wrkCtx.WorkerProfileConfigMapName, wrkCtx.KubeletExtraArgsNodeProfileConfigmapKey))
			}
			extraArgs := map[string]string{}
			if err = json.Unmarshal([]byte(kubeletExtraArgs), &extraArgs); err != nil {
				log.WithError(err).Errorf("failed to unmarshal kubelet extra args content: %w", err)
			}
			profile = &typ.NodeProfile{KubeletConfiguration: []byte(kubeletConfig), KubeletExtraArgs: extraArgs}
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
