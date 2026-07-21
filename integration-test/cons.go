package integration

import (
	"fmt"

	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
)

const (
	kindClusterName                 = "integration-test"
	heirExternalAddress             = "control-plane"
	postgresSecretName              = "postgres-credentials"
	hostClusterKubeConfigPath       = "./kubeconfig.yaml"
	tardigradeClusterKubeConfigPath = "./tardigrade-kubeconfig.yaml"
	heirApiServerPort               = 30080
)

func buildRuntimeConfig() string {
	return `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: my-cluster
  namespace: default
spec:
  controlPlane:
    heir:
      image: localhost:5001/heir-base:v3
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: NodePort
      apiServerNodePort: 30080
  upstreamCluster:
    apiServer:
      externalAddress: "https://` + heirExternalAddress + `:30080"
      sans: ["master0"]
      extraArgs: {}
    controllerManager:
      extraArgs: {}
    scheduler:
      extraArgs: {}
    storage:
      type: kine
      kine:
        dataSourceRef:
          name: ` + postgresSecretName + `
          key: dsn
`
}

func controlPlaneNodeIP() (string, error) {
	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(cmd.NewLogger()),
	)
	nodeList, err := provider.ListNodes(kindClusterName)
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodeList {
		role, err := node.Role()
		if err != nil {
			continue
		}
		if role == "control-plane" {
			ipv4, _, err := node.IP()
			if err != nil {
				return "", fmt.Errorf("failed to get IP for control-plane: %w", err)
			}
			return ipv4, nil
		}
	}

	return "", fmt.Errorf("control-plane node not found for cluster %q", kindClusterName)
}
