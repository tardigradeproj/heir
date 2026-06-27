package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	log "github.com/sirupsen/logrus"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
)

const (
	kindClusterName    = "integration-test"
	postgresSecretName = "postgres-credentials"
)

func checkManagementClusterReadiness(name string, kube *KubeClient, checkDuration time.Duration) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), checkDuration)
	defer cancel()

	for {
		nodes, err := kube.ListNodes(ctx)
		if err == nil {
			allReady := true
			for _, node := range nodes {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
						allReady = false
					}
				}
			}
			if allReady && len(nodes) > 0 {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("cluster %q not ready after %s", name, checkDuration)
		case <-time.After(5 * time.Second):
		}
	}
}

func startManagementCluster(name, kubeconfig string, extraPortMappings []uint32) (string, error) {
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

	if err := provider.Create(
		name,
		cluster.CreateWithRawConfig([]byte(config)),
		cluster.CreateWithKubeconfigPath(kubeconfig),
	); err != nil {
		return "", err
	}

	nodes, err := provider.ListNodes(name)
	if err != nil {
		return "", fmt.Errorf("failed to list nodes for cluster %q: %w", name, err)
	}
	for _, node := range nodes {
		ipv4, _, err := node.IP()
		if err != nil {
			continue
		}
		return ipv4, nil
	}
	return "", fmt.Errorf("no nodes found for cluster %q", name)
}

func provisionPostgres(kube *KubeClient, postgresImage, username, password string, nodePort uint32) (string, error) {
	ctx := context.Background()
	ns := "default"
	labels := map[string]string{"app": "postgres"}
	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     "postgres.default.svc.cluster.local:5432",
		Path:     "/kine",
		RawQuery: "sslmode=disable",
	}).String()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: postgresSecretName, Namespace: ns},
		StringData: map[string]string{"dsn": dsn},
	}
	if err := kube.CreateSecret(ctx, ns, secret); err != nil {
		return "", fmt.Errorf("failed to create secret: %w", err)
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
	if err := kube.CreateDeployment(ctx, ns, deployment); err != nil {
		return "", fmt.Errorf("failed to create deployment: %w", err)
	}

	clusterSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres", Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports:    []corev1.ServicePort{{Port: 5432, TargetPort: intstr.FromInt32(5432)}},
		},
	}
	if err := kube.CreateService(ctx, ns, clusterSvc); err != nil {
		return "", fmt.Errorf("failed to create ClusterIP service: %w", err)
	}

	nodePortSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-nodeport", Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeNodePort,
			Ports:    []corev1.ServicePort{{Port: 5432, TargetPort: intstr.FromInt32(5432), NodePort: int32(nodePort)}},
		},
	}
	if err := kube.CreateService(ctx, ns, nodePortSvc); err != nil {
		return "", fmt.Errorf("failed to create NodePort service: %w", err)
	}

	return dsn, nil
}

func waitForPostgresReady(dsn string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			conn.Close(ctx)
			return nil
		}
		log.WithError(err).Debug("postgres not yet ready, retrying")

		select {
		case <-ctx.Done():
			return fmt.Errorf("postgres not ready after %s: %w", timeout, err)
		case <-time.After(3 * time.Second):
		}
	}
}

func createDatabase(dsn, dbName string) (string, error) {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("failed to connect to postgres: %w", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize())
	if err != nil {
		return "", fmt.Errorf("failed to create database %q: %w", dbName, err)
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("failed to parse dsn: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func buildExternalPostgresDSN(username, password string, nodePort uint32) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     fmt.Sprintf("localhost:%d", nodePort),
		Path:     "/kine",
		RawQuery: "sslmode=disable",
	}).String()
}

func buildInternalPostgresDSN(username, password, dbName string) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(username, password),
		Host:     "postgres.default.svc.cluster.local:5432",
		Path:     "/" + dbName,
		RawQuery: "sslmode=disable",
	}).String()
}

func createKubernetesSecret(kube *KubeClient, namespace, name string, data map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		StringData: data,
	}
	return kube.CreateSecret(context.Background(), namespace, secret)
}

func deleteKubernetesSecret(kube *KubeClient, namespace, name string) error {
	return kube.DeleteSecret(context.Background(), namespace, name)
}

func applyRegistryConfigMap(kube *KubeClient, registryPort uint32) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-registry-hosting",
			Namespace: "kube-public",
		},
		Data: map[string]string{
			"localRegistryHosting.v1": fmt.Sprintf(
				"host: \"localhost:%d\"\nhelp: \"https://kind.sigs.k8s.io/docs/user/local-registry/\"\n",
				registryPort,
			),
		},
	}
	if err := kube.CreateConfigMap(context.Background(), "kube-public", cm); err != nil {
		return fmt.Errorf("failed to create registry ConfigMap: %w", err)
	}
	return nil
}

func deployNginx(kube *KubeClient, namespace string, replicas int32) error {
	labels := map[string]string{"app": "nginx"}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "nginx", Image: "nginx"},
					},
				},
			},
		},
	}
	return kube.CreateDeployment(context.Background(), namespace, dep)
}

func logPodsOutput(kube *KubeClient, namespace, labelSelector string) {
	ctx := context.Background()
	pods, err := kube.ListPods(ctx, namespace, labelSelector)
	if err != nil {
		log.WithError(err).Warn("failed to list pods for log collection")
		return
	}
	for _, pod := range pods {
		logs, err := kube.GetPodLogs(ctx, namespace, pod.Name)
		if err != nil {
			log.WithFields(log.Fields{"pod": pod.Name}).WithError(err).Warn("failed to get pod logs")
			continue
		}
		log.WithFields(log.Fields{"pod": pod.Name}).Infof("pod logs:\n%s", logs)
	}
}

func waitForDeploymentReady(kube *KubeClient, namespace, labelSelector string, expectedCount int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		pods, err := kube.ListPods(ctx, namespace, labelSelector)
		if err == nil {
			ready := 0
			for _, pod := range pods {
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
						ready++
					}
				}
			}
			log.WithFields(log.Fields{"ready": ready, "expected": expectedCount}).Info("waiting for deployment pods to be ready")
			if ready >= expectedCount {
				return nil
			}
		} else {
			log.WithError(err).Debug("failed to list pods, retrying")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%d pod(s) did not become ready after %s", expectedCount, timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

func buildRuntimeConfig(name, namespace, heirImage, konnectivityImage, address, dataSourceRefName string, apiServerPort, konnectivityPort uint32) string {
	return fmt.Sprintf(`apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: %s
  namespace: %s
spec:
  controlPlane:
    heir:
      image: %s
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: NodePort
      apiServerNodePort: %d
  upstreamCluster:
    apiServer:
      sans: ["%s"]
      extraArgs:
        advertise-address: "%s"
    controllerManager:
      extraArgs: {}
    scheduler:
      extraArgs: {}
    controlPlaneEndpoint:
      addresses:
        - "%s"
      apiServer:
        port: %d
      konnectivity:
        port: %d
    network:
      konnectivity:
        server:
          image: %s
    storage:
      type: kine
      kine:
        dataSourceRef:
          name: %s
          key: dsn
`, name, namespace, heirImage, apiServerPort, address, address, address, apiServerPort, konnectivityPort, konnectivityImage, dataSourceRefName)
}

func waitForNodeProfileConfigMap(kube *KubeClient, timeout time.Duration) (typ.NodeProfile, error) {
	wrkCtx := typ.NewWorkerContextWithDefaults()
	const (
		namespace    = "kube-system"
		pollInterval = 5 * time.Second
	)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		cm, err := kube.GetConfigMap(ctx, namespace, wrkCtx.WorkerProfileConfigMapName)
		if err == nil {
			extraArgs := map[string]string{}
			if err := json.Unmarshal([]byte(cm.Data[wrkCtx.KubeletExtraArgsNodeProfileConfigmapKey]), &extraArgs); err != nil {
				return typ.NodeProfile{}, fmt.Errorf("failed to unmarshal kubelet extra args: %w", err)
			}
			endpoint := controlplanev1alpha1.ControlPlaneEndpointSpec{}
			if err := json.Unmarshal([]byte(cm.Data[wrkCtx.ControlPlaneEndpointNodeProfileConfigmapKey]), &endpoint); err != nil {
				return typ.NodeProfile{}, fmt.Errorf("failed to unmarshal control plane endpoint: %w", err)
			}
			profile := typ.NodeProfile{
				KubeletConfiguration: cm.Data[wrkCtx.KubeletConfigurationNodeProfileConfigmapKey],
				KubeletExtraArgs:     extraArgs,
				ControlPlaneEndpoint: endpoint,
				CNIProvider:          cm.Data[wrkCtx.CNIEnableProviderNodeProfileConfigmapKey],
			}
			log.WithFields(log.Fields{"configmap": wrkCtx.WorkerProfileConfigMapName, "namespace": namespace}).Info("node-profile configmap found")
			return profile, nil
		}
		log.WithError(err).Debugf("node-profile configmap not yet available, retrying in %s", pollInterval)

		select {
		case <-ctx.Done():
			return typ.NodeProfile{}, fmt.Errorf("configmap %q not found in %q after %s", wrkCtx.WorkerProfileConfigMapName, namespace, timeout)
		case <-time.After(pollInterval):
		}
	}
}

func waitForControlPlanePodReady(kube *KubeClient, namespace, clusterName string, timeout time.Duration) error {
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s,app.kubernetes.io/managed-by=heir", clusterName)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		pods, err := kube.ListPods(ctx, namespace, labelSelector)
		if err == nil && len(pods) > 0 {
			allReady := true
			for _, pod := range pods {
				for _, cond := range pod.Status.Conditions {
					log.WithFields(log.Fields{
						"condition": cond.Type,
						"status":    cond.Status,
					}).Info("checking control plane condition on management cluster")
					if cond.Type == corev1.PodReady && cond.Status != corev1.ConditionTrue {
						allReady = false
					}
				}
			}
			if allReady {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("control plane pods for %q not ready after %s", clusterName, timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

func waitForNodesReady(kube *KubeClient, count int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		nodes, err := kube.ListNodes(ctx)
		if err == nil {
			ready := 0
			for _, node := range nodes {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						ready++
					}
				}
			}
			log.WithFields(log.Fields{"ready": ready, "expected": count}).Info("waiting for nodes to be ready")
			if ready >= count {
				return nil
			}
		} else {
			log.WithError(err).Debug("failed to list nodes in upstream cluster, retrying")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%d node(s) did not become ready in upstream cluster after %s", count, timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

func waitForNodeNotReady(kube *KubeClient, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		nodes, err := kube.ListNodes(ctx)
		if err == nil && len(nodes) > 0 {
			allNotReady := true
			for _, node := range nodes {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						allNotReady = false
					}
				}
			}
			if allNotReady {
				return nil
			}
		}
		log.Debug("node still reporting ready in upstream cluster, waiting")

		select {
		case <-ctx.Done():
			return fmt.Errorf("node did not become not-ready in upstream cluster after %s", timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

func waitForNodeReady(kube *KubeClient, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		nodes, err := kube.ListNodes(ctx)
		if err == nil && len(nodes) > 0 {
			for _, node := range nodes {
				for _, cond := range node.Status.Conditions {
					log.WithFields(log.Fields{
						"node":      node.Name,
						"condition": cond.Type,
						"status":    cond.Status,
						"reason":    cond.Reason,
						"message":   cond.Message,
					}).Info("node condition")
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						return nil
					}
				}
			}
		} else if err != nil {
			log.WithError(err).Debug("failed to list nodes in upstream cluster, retrying")
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("no node became ready in upstream cluster after %s", timeout)
		case <-time.After(5 * time.Second):
		}
	}
}

func heirBinaryPath() string {
	arch := "amd64_v1"
	if runtime.GOARCH == "arm64" {
		arch = "arm64_v8.0"
	}
	return fmt.Sprintf("../dist/distro_%s_%s/heir", runtime.GOOS, arch)
}

// linuxHeirBinaryPath returns the absolute Linux heir binary path matching the
// host arch, suitable for Docker bind mounts which require absolute paths.
func linuxHeirBinaryPath() (string, error) {
	rel := "../dist/distro_linux_amd64_v1/heir"
	if runtime.GOARCH == "arm64" {
		rel = "../dist/distro_linux_arm64_v8.0/heir"
	}
	return filepath.Abs(rel)
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
