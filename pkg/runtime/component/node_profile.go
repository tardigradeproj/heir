package component

import (
	"bytes"
	"encoding/json"
	"fmt"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	"github.com/tardigrade-runtime/samaritano/pkg/templatewriter"
	"sigs.k8s.io/yaml"
)

func CreateNodeProfileManifest(wrkCtx *typ.WorkerContext, runtime *controlplanev1alpha1.Runtime) ([]byte, error) {
	cfg, err := getNodeProfileConfig(wrkCtx, runtime)
	if err != nil {
		return nil, err
	}
	kubelet := runtime.Spec.UpstreamCluster.Kubelet

	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "node-profile",
		Template: NodeProfileTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write kubelet template: %w", err)
	}

	var cm map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &cm); err != nil {
		return nil, err
	}
	data := cm["data"].(map[string]interface{})

	if kubelet.ConfigPatches != "" {
		kubeletCfgStr := data[wrkCtx.KubeletConfigurationNodeProfileConfigmapKey].(string)
		patched, err := configPatch([]byte(kubeletCfgStr), kubelet.ConfigPatches)
		if err != nil {
			return nil, fmt.Errorf("failed to apply kubelet config patch: %w", err)
		}
		data[wrkCtx.KubeletConfigurationNodeProfileConfigmapKey] = string(patched)
	}

	return yaml.Marshal(cm)
}

func getNodeProfileConfig(wrkCtx *typ.WorkerContext, runtime *controlplanev1alpha1.Runtime) (*NodeProfileConfig, error) {
	coredns := runtime.Spec.UpstreamCluster.Network.Coredns
	kubelet := runtime.Spec.UpstreamCluster.Kubelet
	cni := runtime.Spec.UpstreamCluster.Network.CNI
	controlPlaneEndpoint, err := json.Marshal(runtime.Spec.UpstreamCluster.ControlPlaneEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal API server external addresses: %w", err)
	}
	extraArgs := kubelet.ExtraArgs
	if extraArgs == nil {
		extraArgs = map[string]string{}
	}
	extraArgsYAML, err := yaml.Marshal(extraArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal kubelet extra args: %w", err)
	}

	return &NodeProfileConfig{
		NodeProfileConfigMapName: wrkCtx.WorkerProfileConfigMapName,
		ClientCAFile:             wrkCtx.KubeletPKICaCertPath,
		ClusterDNS:               coredns.ClusterDNSIP,
		ContainerRuntimeEndpoint: wrkCtx.ContainerdAddress,
		KubeletStaticPodPath:     wrkCtx.KubeletStaticPodPath,
		KubeletConfigurationKey:  wrkCtx.KubeletConfigurationNodeProfileConfigmapKey,
		KubeletExtraArgsKey:      wrkCtx.KubeletExtraArgsNodeProfileConfigmapKey,
		KubeletExtraArgs:         string(extraArgsYAML),
		ControlPlaneEndpointKey:  wrkCtx.ControlPlaneEndpointNodeProfileConfigmapKey,
		ControlPlaneEndpoint:     string(controlPlaneEndpoint),
		CNIProvider:              cni.Supplier,
		CNIProviderKey:           wrkCtx.CNIEnableProviderNodeProfileConfigmapKey,
	}, nil
}

type NodeProfileConfig struct {
	NodeProfileConfigMapName string
	ClientCAFile             string
	ClusterDNS               string
	ContainerRuntimeEndpoint string
	KubeletStaticPodPath     string
	KubeletConfigurationKey  string
	KubeletExtraArgsKey      string
	KubeletExtraArgs         string
	ControlPlaneEndpointKey  string
	ControlPlaneEndpoint     string
	CNIProviderKey           string
	CNIProvider              string
}

const NodeProfileTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .NodeProfileConfigMapName }}
  namespace: kube-system
  labels:
    managed-by: bootstrap
data:
  {{ .CNIProviderKey }}: {{ .CNIProvider }}
  {{ .ControlPlaneEndpointKey }}: |
{{ .ControlPlaneEndpoint | indent 4 }}
  {{ .KubeletConfigurationKey }}: |
    apiVersion: kubelet.config.k8s.io/v1beta1
    authentication:
      anonymous:
        enabled: false
      webhook:
        cacheTTL: 0s
        enabled: true
      x509:
        clientCAFile: {{ .ClientCAFile }}
    authorization:
      mode: Webhook
      webhook:
        cacheAuthorizedTTL: 0s
        cacheUnauthorizedTTL: 0s
    resolvConf: /run/systemd/resolve/resolv.conf
    cgroupDriver: systemd
    enforceNodeAllocatable: []
    clusterDNS:
    - {{ .ClusterDNS }}
    clusterDomain: cluster.local
    containerRuntimeEndpoint: {{ .ContainerRuntimeEndpoint }}
    evictionHard:
      imagefs.available: 0%
      nodefs.available: 0%
      nodefs.inodesFree: 0%
    failSwapOn: false
    healthzBindAddress: 0.0.0.0
    healthzPort: 10248
    imageGCHighThresholdPercent: 100
    kind: KubeletConfiguration
    serverTLSBootstrap: true
    rotateCertificates: true
    staticPodPath: {{ .KubeletStaticPodPath }}
  {{ .KubeletExtraArgsKey }}: |
{{ .KubeletExtraArgs | indent 4 -}}
`
