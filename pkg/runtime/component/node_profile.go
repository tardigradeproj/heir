package component

import (
	"bytes"
	"fmt"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	"github.com/tardigrade-runtime/samaritano/pkg/templatewriter"
)

func CreateKubeletManifest(wrkCtx *typ.WorkerContext, runtime *controlplanev1alpha1.Runtime) ([]byte, error) {
	cfg := getNodeProfileConfig(wrkCtx, runtime)
	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "node-profile",
		Template: NodeProfileTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write kubelet template: %w", err)
	}
	return buf.Bytes(), nil
}

func getNodeProfileConfig(wrkCtx *typ.WorkerContext, runtime *controlplanev1alpha1.Runtime) *NodeProfileConfig {
	coredns := runtime.Spec.UpstreamCluster.Network.Coredns
	return &NodeProfileConfig{
		NodeProfileConfigMapName: wrkCtx.WorkerProfileConfigMapName,
		ClientCAFile:             wrkCtx.KubeletPKICaCertPath,
		ClusterDNS:               coredns.ClusterDNSIP,
		ContainerRuntimeEndpoint: wrkCtx.ContainerdAddress,
		KubeletStaticPodPath:     wrkCtx.KubeletStaticPodPath,
	}
}

type NodeProfileConfig struct {
	NodeProfileConfigMapName string
	ClientCAFile             string
	ClusterDNS               string
	ContainerRuntimeEndpoint string
	KubeletStaticPodPath     string
}

const NodeProfileTemplate = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .NodeProfileConfigMapName }}
  namespace: kube-system
data:
  kubelet.configuration: |
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
	cgroupDriver: systemd
	cgroupRoot: /kubelet
	clusterDNS:
	- {{ .ClusterDNS }}
	clusterDomain: cluster.local
	containerRuntimeEndpoint: {{ .ContainerRuntimeEndpoint }}
	cpuManagerReconcilePeriod: 0s
	crashLoopBackOff: {}
	evictionHard:
	  imagefs.available: 0%
	  nodefs.available: 0%
	  nodefs.inodesFree: 0%
	evictionPressureTransitionPeriod: 0s
	failSwapOn: false
	fileCheckFrequency: 0s
	healthzBindAddress: 0.0.0.0
	healthzPort: 10248
	httpCheckFrequency: 0s
	imageGCHighThresholdPercent: 100
	imageMaximumGCAge: 0s
	imageMinimumGCAge: 0s
	kind: KubeletConfiguration
	logging:
	  flushFrequency: 0
	  options:
		json:
		  infoBufferSize: "0"
		text:
		  infoBufferSize: "0"
	  verbosity: 0
	memorySwap: {}
	nodeStatusReportFrequency: 0s
	nodeStatusUpdateFrequency: 0s
	rotateCertificates: true
	runtimeRequestTimeout: 0s
	shutdownGracePeriod: 0s
	shutdownGracePeriodCriticalPods: 0s
	staticPodPath: {{ .KubeletStaticPodPath }}
	streamingConnectionIdleTimeout: 0s
	syncFrequency: 0s
	volumeStatsAggPeriod: 0s	
`
