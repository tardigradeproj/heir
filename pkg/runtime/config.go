package runtime

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	"github.com/tardigradeproj/heir/pkg/runtime/component"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GenerateControlPlaneConfig builds the <name>-config ConfigMap that holds the s6-overlay run
// scripts for every supervised process, and returns the ConfigMap together with the hex-encoded
// SHA-256 hash of its data. No API calls are made; the caller is responsible for setting the
// owner reference and persisting the result.
func GenerateControlPlaneConfig(runtime *controlplanev1alpha1.Runtime, layout ControlPlaneLayout) (*corev1.ConfigMap, string, error) {
	workerProfile := typ.NewWorkerContextWithDefaults()
	net := runtime.Spec.UpstreamCluster.Network
	kubeproxy, err := component.CreateKubeProxyManifest(runtime)
	if err != nil {
		return nil, "", err
	}
	tlsbootstrap := component.CreateBootstrapManifest()
	coredns, err := component.CreateCorednsManifest(runtime)
	if err != nil {
		return nil, "", err
	}
	nodeProfile, err := component.CreateNodeProfileManifest(workerProfile, runtime)
	if err != nil {
		return nil, "", err
	}

	apiServerParameters := map[string]string{
		// kubelet-preferred-address-types=Hostname ensures the API server connects to worker nodes using their registered hostname. The hostname is used
		//as the TLS ServerName (SNI), which the kubelet's serving certificate always carries as a hostname SAN.
		"kubelet-preferred-address-types":  "Hostname",
		"advertise-address":                "",
		"allow-privileged":                 "true",
		"endpoint-reconciler-type":         "none",
		"authorization-mode":               "Node,RBAC",
		"bind-address":                     "0.0.0.0",
		"client-ca-file":                   layout.PKI.CACert.MountPath,
		"enable-admission-plugins":         "NamespaceLifecycle,NodeRestriction,LimitRanger,ServiceAccount,DefaultStorageClass,ResourceQuota",
		"etcd-servers":                     "http://127.0.0.1:2379",
		"enable-bootstrap-token-auth":      "true",
		"event-ttl":                        "1h",
		"kubelet-certificate-authority":    layout.PKI.CACert.MountPath,
		"kubelet-client-certificate":       layout.PKI.APIServerCert.MountPath,
		"kubelet-client-key":               layout.PKI.APIServerKey.MountPath,
		"runtime-config":                   "api/all=true",
		"service-account-issuer":           "https://kubernetes.default.svc.cluster.local",
		"service-account-key-file":         layout.PKI.ServiceAccountCert.MountPath,
		"service-account-signing-key-file": layout.PKI.ServiceAccountKey.MountPath,
		"service-cluster-ip-range":         net.ServiceCIDR,
		"service-node-port-range":          "30000-32767",
		"tls-cert-file":                    layout.PKI.APIServerCert.MountPath,
		"tls-private-key-file":             layout.PKI.APIServerKey.MountPath,
		"v":                                "2",
	}
	if runtime.Spec.UpstreamCluster.Network.Konnectivity.Enabled {
		apiServerParameters["egress-selector-config-file"] = layout.Config.Konnectivity.MountPath
	}
	apiserverScript := RenderRunScript("/usr/local/bin/kube-apiserver",
		MergeArgs(apiServerParameters, runtime.Spec.UpstreamCluster.APIServer.ExtraArgs),
	)

	controllerManagerScript := RenderRunScript("/usr/local/bin/kube-controller-manager",
		MergeArgs(map[string]string{
			"allocate-node-cidrs":              "true",
			"bind-address":                     "0.0.0.0",
			"cluster-cidr":                     net.PodCIDR,
			"cluster-name":                     "tardigrade",
			"cluster-signing-cert-file":        layout.PKI.CACert.MountPath,
			"cluster-signing-key-file":         layout.PKI.CAKey.MountPath,
			"kubeconfig":                       layout.Auth.ControllerManagerConf.MountPath,
			"root-ca-file":                     layout.PKI.CACert.MountPath,
			"service-account-private-key-file": layout.PKI.ServiceAccountKey.MountPath,
			"service-cluster-ip-range":         net.ServiceCIDR,
			"use-service-account-credentials":  "true",
			"controllers":                      "*,tokencleaner",
			"v":                                "2",
		}, runtime.Spec.UpstreamCluster.ControllerManager.ExtraArgs),
	)

	schedulerScript := RenderRunScript("/usr/local/bin/kube-scheduler",
		MergeArgs(map[string]string{
			"authentication-kubeconfig": layout.Auth.SchedulerConf.MountPath,
			"authorization-kubeconfig":  layout.Auth.SchedulerConf.MountPath,
			"bind-address":              "127.0.0.1",
			"kubeconfig":                layout.Auth.SchedulerConf.MountPath,
			"leader-elect":              "true",
		}, runtime.Spec.UpstreamCluster.Scheduler.ExtraArgs),
	)

	data := map[string]string{
		layout.Config.APIServer.SecretKey:           apiserverScript,
		layout.Config.ControllerManager.SecretKey:   controllerManagerScript,
		layout.Config.Scheduler.SecretKey:           schedulerScript,
		layout.StaticManifest.Bootstrap.SecretKey:   string(tlsbootstrap),
		layout.StaticManifest.NodeProfile.SecretKey: string(nodeProfile),
		layout.StaticManifest.Coredns.SecretKey:     string(coredns),
		layout.StaticManifest.KubeProxy.SecretKey:   string(kubeproxy),
	}
	if runtime.Spec.UpstreamCluster.Network.Konnectivity.Enabled {
		data[layout.Config.Konnectivity.SecretKey] = renderKonnectivityService(workerProfile.KonnectivityUdsName)
		konnectivityAgent, err := component.CreateKonnectivityAgentManifest(runtime, workerProfile)
		if err != nil {
			return nil, "", err
		}
		data[layout.StaticManifest.KonnectivityAgent.SecretKey] = string(konnectivityAgent)
	}
	if runtime.Spec.UpstreamCluster.Network.CNI.Supplier == "flannel" {
		flannelConfig, err := component.CreateFlannelCNIManifest(runtime)
		if err != nil {
			return nil, "", err
		}
		data[layout.StaticManifest.FlannelCNI.SecretKey] = string(flannelConfig)
	}

	desiredHash, err := HashConfigData(data)
	if err != nil {
		return nil, "", err
	}

	labels := map[string]string{
		"app.kubernetes.io/name":       runtime.Name,
		"app.kubernetes.io/managed-by": "heir",
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtime.Name,
			Namespace: runtime.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"controlplane.tardigrade.runtime.io/deletion-protection": "false",
			},
		},
		Data: data,
	}

	return cm, desiredHash, nil
}

// HashConfigData returns the hex-encoded SHA-256 digest of a ConfigMap data map.
// encoding/json marshals map keys in sorted order, ensuring a deterministic digest.
func HashConfigData(data map[string]string) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

// MergeArgs returns a new map with all entries from defaults overridden by any matching key in extra.
func MergeArgs(defaults, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(extra))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}
func renderKonnectivityService(udsName string) string {
	return fmt.Sprintf(`
apiVersion: apiserver.k8s.io/v1beta1
kind: EgressSelectorConfiguration
egressSelections:
- name: cluster
  connection:
    proxyProtocol: GRPC
    transport:
      uds:
        udsName: %s
`, udsName)
}

// RenderRunScript produces a shell run-script for the given binary and args.
// Args are emitted in sorted order for deterministic output.
func RenderRunScript(binary string, args map[string]string) string {
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	if len(args) == 0 {
		sb.WriteString("exec " + binary)
		return sb.String()
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sb.WriteString("exec " + binary + " \\\n")
	for i, k := range keys {
		if i < len(keys)-1 {
			fmt.Fprintf(&sb, "  --%s=%s \\\n", k, args[k])
		} else {
			fmt.Fprintf(&sb, "  --%s=%s", k, args[k])
		}
	}
	return sb.String()
}
