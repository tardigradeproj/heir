package component

import (
	"bytes"
	"encoding/json"
	"fmt"

	log "github.com/sirupsen/logrus"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/templatewriter"
)

func CreateKubeProxyManifest(runtime *controlplanev1alpha1.Runtime) ([]byte, error) {
	cfg, err := getConfig(runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube-proxy template configuration: %w", err)
	}
	if cfg == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "kube-proxy",
		Template: proxyTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write kube-proxy template: %w", err)
	}
	return buf.Bytes(), nil
}

func getConfig(runtime *controlplanev1alpha1.Runtime) (*proxyConfig, error) {
	if runtime.Spec.UpstreamCluster.Network.KubeProxy.Disabled {
		return nil, nil
	}
	network := runtime.Spec.UpstreamCluster.Network
	args := map[string]string{
		"config":            "/var/lib/kube-proxy/config.conf",
		"hostname-override": "$(NODE_NAME)",
	}

	for name, value := range network.KubeProxy.ExtraArgs {
		if _, ok := args[name]; ok {
			log.Warnf("overriding kube-proxy flag with user provided value: %s", name)
		}
		args[name] = value
	}
	toDashedArg := func(p map[string]string) []string {
		v := make([]string, 0, len(p))
		for name, value := range p {
			v = append(v, fmt.Sprintf("--%s=%s", name, value))
		}
		return v
	}
	cfg := proxyConfig{
		Enabled:              true,
		ClusterCIDR:          network.PodCIDR,
		ControlPlaneEndpoint: "https://127.0.0.1:6443",
		Image:                fmt.Sprintf("%s/%s", network.KubeProxy.RegisterSetting.Registry, network.KubeProxy.RegisterSetting.Image),
		PullPolicy:           string(network.KubeProxy.RegisterSetting.PullPolicy),
		DualStack:            false,
		Mode:                 network.KubeProxy.Mode,
		MetricsBindAddress:   network.KubeProxy.MetricsBindAddress,
		Args:                 toDashedArg(args),
	}
	nodePortAddresses, err := json.Marshal(network.KubeProxy.NodePortAddresses)
	if err != nil {
		return nil, err
	}
	cfg.NodePortAddresses = string(nodePortAddresses)

	iptables, err := json.Marshal(network.KubeProxy.IPTables)
	if err != nil {
		return nil, err
	}
	cfg.IPTables = string(iptables)

	ipvs, err := json.Marshal(network.KubeProxy.IPVS)
	if err != nil {
		return nil, err
	}
	cfg.IPVS = string(ipvs)

	nftables, err := json.Marshal(network.KubeProxy.NFTables)
	if err != nil {
		return nil, err
	}
	cfg.NFTables = string(nftables)
	return &cfg, nil
}

type proxyConfig struct {
	Enabled              bool
	DualStack            bool
	ControlPlaneEndpoint string
	ClusterCIDR          string
	Image                string
	PullPolicy           string
	Mode                 string
	MetricsBindAddress   string
	IPTables             string
	IPVS                 string
	NFTables             string
	NodePortAddresses    string
	Args                 []string
}

const proxyTemplate = `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-proxy
  namespace: kube-system
  labels:
    managed-by: bootstrap
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
    managed-by: bootstrap
  name: kube-proxy
  namespace: kube-system
rules:
  - apiGroups: [""]
    verbs: ["get"]
    resources: ["configmaps"]
    resourceNames: ["kube-proxy"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
    managed-by: bootstrap
  name: node-proxier
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:node-proxier
subjects:
- kind: ServiceAccount
  name: kube-proxy
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
    managed-by: bootstrap
  name: kube-proxy
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: kube-proxy
subjects:
- kind: Group
  name: system:bootstrappers:worker
  apiGroup: rbac.authorization.k8s.io

---
kind: ConfigMap
apiVersion: v1
metadata:
  name: kube-proxy
  namespace: kube-system
  labels:
    app: kube-proxy
    managed-by: bootstrap
data:
  kubeconfig.conf: |-
    apiVersion: v1
    kind: Config
    clusters:
    - cluster:
        certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        server: {{ .ControlPlaneEndpoint }}
      name: default
    contexts:
    - context:
        cluster: default
        namespace: default
        user: default
      name: default
    current-context: default
    users:
    - name: default
      user:
        tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
  config.conf: |-
    apiVersion: kubeproxy.config.k8s.io/v1alpha1
    bindAddress: 0.0.0.0
    clientConnection:
      acceptContentTypes: ""
      burst: 0
      contentType: ""
      kubeconfig: /var/lib/kube-proxy/kubeconfig.conf
      qps: 0
    clusterCIDR: {{ .ClusterCIDR }}
    configSyncPeriod: 0s
    mode: "{{ .Mode }}"
    conntrack:
      maxPerCore: 0
      min: null
      tcpCloseWaitTimeout: null
      tcpEstablishedTimeout: null
    detectLocalMode: ""
    enableProfiling: false
    healthzBindAddress: ""
    hostnameOverride: ""
    iptables: {{ .IPTables }}
    ipvs: {{ .IPVS }}
    nftables: {{ .NFTables }}
    kind: KubeProxyConfiguration
    metricsBindAddress: {{ .MetricsBindAddress }}
    nodePortAddresses: {{ .NodePortAddresses }}
    oomScoreAdj: null
    portRange: ""
    showHiddenMetricsForVersion: ""
    udpIdleTimeout: 0s
    winkernel:
      enableDSR: false
      networkName: ""
      sourceVip: ""
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  labels:
    k8s-app: kube-proxy
    managed-by: bootstrap
  name: kube-proxy
  namespace: kube-system
spec:
  selector:
    matchLabels:
      k8s-app: kube-proxy
  updateStrategy:
    type: RollingUpdate
  template:
    metadata:
      labels:
        k8s-app: kube-proxy
      annotations:
        prometheus.io/scrape: 'true'
        prometheus.io/port: '10249'
    spec:
      priorityClassName: system-node-critical
      containers:
      - name: kube-proxy
        image: {{ .Image }}
        imagePullPolicy: {{ .PullPolicy }}
        command:
        - /usr/local/bin/kube-proxy
        args:
        {{ range .Args}}
        - {{ . }}
        {{ end }}
        securityContext:
          privileged: true
        volumeMounts:
        - mountPath: /var/lib/kube-proxy
          name: kube-proxy
        - mountPath: /run/xtables.lock
          name: xtables-lock
          readOnly: false
        - mountPath: /lib/modules
          name: lib-modules
          readOnly: true
        env:
          - name: NODE_NAME
            valueFrom:
              fieldRef:
                fieldPath: spec.nodeName
      hostNetwork: true
      serviceAccountName: kube-proxy
      volumes:
      - name: kube-proxy
        configMap:
          name: kube-proxy
      - name: xtables-lock
        hostPath:
          path: /run/xtables.lock
          type: FileOrCreate
      - name: lib-modules
        hostPath:
          path: /lib/modules
      tolerations:
      - operator: Exists
        effect: NoExecute
      - operator: Exists
        effect: NoSchedule
      nodeSelector:
        kubernetes.io/os: linux
`
