package component

import (
	"bytes"
	"fmt"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/templatewriter"
	"k8s.io/utils/ptr"
)

func CreateCorednsManifest(runtime *controlplanev1alpha1.Runtime) ([]byte, error) {
	cfg := getCoreDnsConfig(runtime)
	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "coredns",
		Template: coreDNSTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write coredns template: %w", err)
	}
	return buf.Bytes(), nil
}

func getCoreDnsConfig(runtime *controlplanev1alpha1.Runtime) *coreDNSConfig {
	coredns := runtime.Spec.UpstreamCluster.Network.Coredns
	cfg := &coreDNSConfig{
		Replicas:                   int(*coredns.Replicas),
		ClusterDNSIP:               coredns.ClusterDNSIP,
		ClusterDomain:              "cluster.local",
		Image:                      fmt.Sprintf("%s/%s", coredns.RegisterSetting.Registry, coredns.RegisterSetting.Image),
		PullPolicy:                 string(coredns.RegisterSetting.PullPolicy),
		MaxUnavailableReplicas:     ptr.To(uint(1)),
		DisablePodAntiAffinity:     true,
		DisablePodDisruptionBudget: true,
	}
	if cfg.Replicas <= 1 {
		cfg.MaxUnavailableReplicas = ptr.To(uint(0))
	}
	return cfg
}

type coreDNSConfig struct {
	Replicas                   int
	ClusterDNSIP               string
	ClusterDomain              string
	Image                      string
	PullPolicy                 string
	MaxUnavailableReplicas     *uint
	DisablePodAntiAffinity     bool
	DisablePodDisruptionBudget bool
}

const coreDNSTemplate = `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: coredns
  namespace: kube-system
  labels:
    managed-by: bootstrap
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
    managed-by: bootstrap
  name: system-coredns
rules:
- apiGroups:
  - ""
  resources:
  - endpoints
  - services
  - pods
  - namespaces
  verbs:
  - list
  - watch
- apiGroups:
  - discovery.k8s.io
  resources:
  - endpointslices
  verbs:
  - list
  - watch
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  labels:
    kubernetes.io/bootstrapping: rbac-defaults
    managed-by: bootstrap
  name: system-coredns
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system-coredns
subjects:
- kind: ServiceAccount
  name: coredns
  namespace: kube-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns
  namespace: kube-system
  labels:
    managed-by: bootstrap
data:
  Corefile: |
    .:53 {
        errors
        health
        ready
        kubernetes {{ .ClusterDomain }} in-addr.arpa ip6.arpa {
          pods insecure
          ttl 30
          fallthrough in-addr.arpa ip6.arpa
        }
        prometheus :9153
        forward . /etc/resolv.conf
        cache 30
        loop
        reload
        loadbalance
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coredns
  namespace: kube-system
  labels:
    k8s-app: kube-dns
    kubernetes.io/name: "CoreDNS"
    managed-by: bootstrap
spec:
  replicas: {{ .Replicas }}
  strategy:
    type: RollingUpdate
    {{- if .MaxUnavailableReplicas }}
    rollingUpdate:
      maxUnavailable: {{ .MaxUnavailableReplicas }}
    {{- end }}
  selector:
    matchLabels:
      k8s-app: kube-dns
  template:
    metadata:
      labels:
        k8s-app: kube-dns
      annotations:
        prometheus.io/scrape: 'true'
        prometheus.io/port: '9153'
    spec:
      priorityClassName: system-cluster-critical
      serviceAccountName: coredns
      tolerations:
      - key: node-role.kubernetes.io/master
        operator: Exists
        effect: NoSchedule
      - key: node-role.kubernetes.io/control-plane
        operator: Exists
        effect: NoSchedule
      nodeSelector:
        kubernetes.io/os: linux
      {{- if not .DisablePodAntiAffinity }}
      # Require running coredns replicas on different nodes
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
          - topologyKey: "kubernetes.io/hostname"
            labelSelector:
              matchExpressions:
              - key: k8s-app
                operator: In
                values: ['kube-dns']
      {{- end }}
      topologySpreadConstraints:
      - topologyKey: topology.kubernetes.io/zone
        maxSkew: 1
        whenUnsatisfiable: ScheduleAnyway
        labelSelector:
          matchLabels:
            k8s-app: kube-dns
      containers:
      - name: coredns
        image: {{ .Image }}
        imagePullPolicy: {{ .PullPolicy }}
        resources:
          requests:
            cpu: 100m
            memory: 70Mi
        args: [ "-conf", "/etc/coredns/Corefile" ]
        volumeMounts:
        - name: config-volume
          mountPath: /etc/coredns
          readOnly: true
        ports:
        - containerPort: 53
          name: dns
          protocol: UDP
        - containerPort: 53
          name: dns-tcp
          protocol: TCP
        - containerPort: 9153
          name: metrics
          protocol: TCP
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            add:
            - NET_BIND_SERVICE
            drop:
            - all
          readOnlyRootFilesystem: true
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
            scheme: HTTP
          initialDelaySeconds: 60
          periodSeconds: 10
          timeoutSeconds: 1
          successThreshold: 1
          failureThreshold: 3
        readinessProbe:
          httpGet:
            path: /ready
            port: 8181
            scheme: HTTP
          initialDelaySeconds: 30 # give loop plugin time to detect loops
          periodSeconds: 2
          timeoutSeconds: 1
          successThreshold: 1
          failureThreshold: 3
      dnsPolicy: Default
      volumes:
        - name: config-volume
          configMap:
            name: coredns
            items:
            - key: Corefile
              path: Corefile
{{- if not .DisablePodDisruptionBudget }}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: coredns
  namespace: kube-system
  labels:
    k8s-app: kube-dns
    kubernetes.io/name: "CoreDNS"
    managed-by: bootstrap
spec:
  minAvailable: 50%
  selector:
    matchLabels:
      k8s-app: kube-dns
{{- end }}
---
apiVersion: v1
kind: Service
metadata:
  name: kube-dns
  namespace: kube-system
  annotations:
    prometheus.io/port: "9153"
    prometheus.io/scrape: "true"
  labels:
    k8s-app: kube-dns
    kubernetes.io/cluster-service: "true"
    kubernetes.io/name: "CoreDNS"
    managed-by: bootstrap
spec:
  selector:
    k8s-app: kube-dns
  clusterIP: {{ .ClusterDNSIP }}
  ports:
  - name: dns
    port: 53
    protocol: UDP
  - name: dns-tcp
    port: 53
    protocol: TCP
  - name: metrics
    port: 9153
    protocol: TCP
`
