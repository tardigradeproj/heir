package integration

import (
	"context"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
)

func setupLocalRegistry(registryName string, registryPort uint32) error {
	const (
		registryImage = "registry:2"
		kindNetwork   = "kind"
	)

	hostPort := fmt.Sprintf("%d", registryPort)

	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, registryName)
	if cerrdefs.IsNotFound(err) {
		reader, err := cli.ImagePull(ctx, registryImage, image.PullOptions{})
		if err != nil {
			return fmt.Errorf("failed to pull %s: %w", registryImage, err)
		}
		io.Copy(io.Discard, reader)
		reader.Close()

		resp, err := cli.ContainerCreate(ctx,
			&container.Config{
				Image:        registryImage,
				ExposedPorts: nat.PortSet{"5000/tcp": struct{}{}},
			},
			&container.HostConfig{
				RestartPolicy: container.RestartPolicy{Name: "always"},
				PortBindings: nat.PortMap{
					"5000/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
				},
			},
			nil, nil, registryName,
		)
		if err != nil {
			return fmt.Errorf("failed to create registry container: %w", err)
		}
		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("failed to start registry container: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to inspect registry container: %w", err)
	} else if !inspect.State.Running {
		if err := cli.ContainerStart(ctx, registryName, container.StartOptions{}); err != nil {
			return fmt.Errorf("failed to start existing registry container: %w", err)
		}
	}

	if err := cli.NetworkConnect(ctx, kindNetwork, registryName, nil); err != nil {
		if !strings.Contains(err.Error(), "already exists in network") {
			return fmt.Errorf("failed to connect registry to %s network: %w", kindNetwork, err)
		}
	}

	return nil
}

func configureRegistryOnNodes(clusterName, registryName string, registryPort uint32) error {
	registryDir := fmt.Sprintf("/etc/containerd/certs.d/localhost:%d", registryPort)
	hostsToml := fmt.Sprintf(`server = "https://localhost:%d"

[host."http://%s:5000"]
  capabilities = ["pull", "resolve"]
`, registryPort, registryName)

	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(cmd.NewLogger()),
	)
	nodes, err := provider.ListNodes(clusterName)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	for _, node := range nodes {
		if err := node.Command("mkdir", "-p", registryDir).Run(); err != nil {
			return fmt.Errorf("failed to create registry dir on node %s: %w", node, err)
		}
		if err := node.Command("cp", "/dev/stdin", registryDir+"/hosts.toml").
			SetStdin(strings.NewReader(hostsToml)).
			Run(); err != nil {
			return fmt.Errorf("failed to write hosts.toml on node %s: %w", node, err)
		}
	}

	return nil
}

func teardownLocalRegistry(registryName string) error {
	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	if err := cli.ContainerRemove(ctx, registryName, container.RemoveOptions{Force: true}); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("failed to remove registry container: %w", err)
		}
	}
	return nil
}

func pushImageToRegistry(sourceImage, targetRef string, registryPort uint32) error {
	localTag := fmt.Sprintf("localhost:%d/%s", registryPort, targetRef)

	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	reader, err := cli.ImagePull(ctx, sourceImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull %s: %w", sourceImage, err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	if err := cli.ImageTag(ctx, sourceImage, localTag); err != nil {
		return fmt.Errorf("failed to tag %s as %s: %w", sourceImage, localTag, err)
	}

	// "e30=" is base64("{}") — empty auth for unauthenticated local registry
	reader, err = cli.ImagePush(ctx, localTag, image.PushOptions{RegistryAuth: "e30="})
	if err != nil {
		return fmt.Errorf("failed to push %s: %w", localTag, err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	return nil
}

func applyRegistryConfigMap(kubeconfigPath string, registryPort uint32) error {
	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to build rest config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create k8s clientset: %w", err)
	}

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

	_, err = clientset.CoreV1().ConfigMaps("kube-public").Create(context.Background(), cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create registry ConfigMap: %w", err)
	}
	return nil
}
