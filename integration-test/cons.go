package integration

import (
	"context"
	"fmt"
	"net/url"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

func checkManagementClusterReadiness(name, kubeconfigPath string, checkDuration time.Duration) error {
	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(cmd.NewLogger()),
	)
	nodeList, err := provider.ListNodes(name)
	if err != nil {
		return err
	}
	if len(nodeList) == 0 {
		return fmt.Errorf("cluster %q has no nodes", name)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to build rest config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), checkDuration)
	defer cancel()

	for {
		nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err == nil {
			allReady := true
			for _, node := range nodes.Items {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
						allReady = false
					}
				}
			}
			if allReady && len(nodes.Items) > 0 {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("cluster %q not ready after 80s", name)
		case <-time.After(5 * time.Second):
		}
	}
}
func startManagementCluster(name, kubeconfig string, extraPortMappings []uint32) error {
	portMappings := ""
	for _, port := range extraPortMappings {
		portMappings += fmt.Sprintf("  - containerPort: %d\n    hostPort: %d\n", port, port)
	}

	config := `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
kubeadmConfigPatches:
  - |-
    kind: ClusterConfiguration
    apiServer:
      certSANs:
        - "host.docker.internal"
        - "kind-control-plane"
        - "kubernetes"
        - "kubernetes.default"
        - "kubernetes.default.svc"
        - "kubernetes.default.svc.cluster.local"
        - "localhost"
        - "10.0.2.2"
        - "10.96.0.1"
        - "172.19.0.2"
        - "127.0.0.1"
        - "control-plane"
nodes:
- role: control-plane
  extraPortMappings:
` + portMappings + `containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
`

	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(cmd.NewLogger()),
	)

	return provider.Create(
		name,
		cluster.CreateWithRawConfig([]byte(config)),
		cluster.CreateWithKubeconfigPath(kubeconfig),
	)
}
func provisionPostgres(kubeconfigPath, postgresImage, username, password string, nodePort uint32) error {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to build rest config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	ctx := context.Background()
	ns := "default"
	labels := map[string]string{"app": "postgres"}
	dsn := (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password),
		Host:   "postgres.default.svc.cluster.local:5432",
		Path:   "/kine?sslmode=disable",
	}).String()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: postgresSecretName, Namespace: ns},
		StringData: map[string]string{"dsn": dsn},
	}
	if _, err := clientset.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "postgres",
						Image: postgresImage,
						Ports: []corev1.ContainerPort{{ContainerPort: 5432}},
						Env: []corev1.EnvVar{
							{Name: "POSTGRES_USER", Value: username},
							{Name: "POSTGRES_PASSWORD", Value: password},
							{Name: "POSTGRES_DB", Value: "kine"},
						},
					}},
				},
			},
		},
	}
	if _, err := clientset.AppsV1().Deployments(ns).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	clusterSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports:    []corev1.ServicePort{{Port: 5432, TargetPort: intstr.FromInt32(5432)}},
		},
	}
	if _, err := clientset.CoreV1().Services(ns).Create(ctx, clusterSvc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create ClusterIP service: %w", err)
	}

	nodePortSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-nodeport", Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeNodePort,
			Ports:    []corev1.ServicePort{{Port: 5432, TargetPort: intstr.FromInt32(5432), NodePort: int32(nodePort)}},
		},
	}
	if _, err := clientset.CoreV1().Services(ns).Create(ctx, nodePortSvc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create NodePort service: %w", err)
	}

	return nil
}

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
