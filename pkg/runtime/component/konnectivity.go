package component

import (
	"bytes"
	"fmt"
	"net"
	"sort"

	log "github.com/sirupsen/logrus"
	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	"github.com/tardigrade-runtime/samaritano/pkg/templatewriter"
	"sigs.k8s.io/yaml"
)

func CreateKonnectivityAgentManifest(runtime *controlplanev1alpha1.Runtime, wrkCtx *typ.WorkerContext) ([]byte, error) {

	cfg, err := getKonnectivityAgentConfig(runtime, wrkCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build konnectivity agent config: %w", err)
	}
	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "konnectivity-agent",
		Template: konnectivityAgentTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write konnectivity agent template: %w", err)
	}
	return buf.Bytes(), nil
}

func getKonnectivityAgentConfig(runtime *controlplanev1alpha1.Runtime, wrkCtx *typ.WorkerContext) (*konnectivityAgentConfig, error) {
	agent := runtime.Spec.UpstreamCluster.Network.Konnectivity.KonnectivityAgentSpec
	cpe := runtime.Spec.UpstreamCluster.ControlPlaneEndpoint

	if len(cpe.Addresses) == 0 {
		return nil, fmt.Errorf("controlPlaneEndpoint.addresses must not be empty")
	}
	host, port, err := net.SplitHostPort(wrkCtx.KonnectivityWorkerProxyServerAddress)
	if err != nil {
		log.WithError(err).
			WithField("address", wrkCtx.KonnectivityWorkerProxyServerAddress).
			Error("failed to split konnectivity worker proxy server address")
		return nil, fmt.Errorf("failed to read konnectivity proxy server address: %w", err)
	}
	defaults := map[string]string{
		"logtostderr":                "true",
		"ca-cert":                    "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		"proxy-server-host":          host,
		"proxy-server-port":          port,
		"admin-server-port":          "8133",
		"health-server-port":         "8134",
		"service-account-token-path": "/var/run/secrets/tokens/konnectivity-agent-token",
	}
	for k, v := range agent.ExtraArgs {
		defaults[k] = v
	}

	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(defaults))
	for _, k := range keys {
		args = append(args, fmt.Sprintf("--%s=%s", k, defaults[k]))
	}

	// Use sigs.k8s.io/yaml so JSON struct tags on corev1.Toleration are respected.
	tolerationsYAML, err := yaml.Marshal(agent.Tolerations)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tolerations: %w", err)
	}

	replicas := int32(1)
	if agent.Replicas != nil {
		replicas = *agent.Replicas
	}

	return &konnectivityAgentConfig{
		Image:           agent.Image,
		Mode:            string(agent.Mode),
		Replicas:        replicas,
		HostNetwork:     agent.HostNetwork,
		TolerationsYAML: string(tolerationsYAML),
		Args:            args,
	}, nil
}

type konnectivityAgentConfig struct {
	Image           string
	Mode            string
	Replicas        int32
	HostNetwork     bool
	TolerationsYAML string
	Args            []string
}

const konnectivityAgentTemplate = `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:konnectivity-server
  labels:
    kubernetes.io/cluster-service: "true"
    managed-by: bootstrap
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: User
    name: system:konnectivity-server
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: konnectivity-agent
  namespace: kube-system
  labels:
    kubernetes.io/cluster-service: "true"
    managed-by: bootstrap
---
apiVersion: apps/v1
kind: {{ .Mode }}
metadata:
  name: konnectivity-agent
  namespace: kube-system
  labels:
    k8s-app: konnectivity-agent
    managed-by: bootstrap
spec:
  selector:
    matchLabels:
      k8s-app: konnectivity-agent
  {{- if eq .Mode "Deployment" }}
  replicas: {{ .Replicas }}
  {{- end }}
  template:
    metadata:
      labels:
        k8s-app: konnectivity-agent
    spec:
      priorityClassName: system-cluster-critical
      hostNetwork: {{ .HostNetwork }}
      tolerations:
{{ .TolerationsYAML | indent 6 }}
      containers:
        - name: konnectivity-agent
          image: {{ .Image }}
          command: ["/proxy-agent"]
          args:
            {{- range .Args }}
            - {{ . }}
            {{- end }}
          livenessProbe:
            httpGet:
              port: 8134
              path: /healthz
            initialDelaySeconds: 15
            timeoutSeconds: 15
          volumeMounts:
            - mountPath: /var/run/secrets/tokens
              name: konnectivity-agent-token
      serviceAccountName: konnectivity-agent
      volumes:
        - name: konnectivity-agent-token
          projected:
            sources:
              - serviceAccountToken:
                  path: konnectivity-agent-token
                  audience: system:konnectivity-server
`
