package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	"github.com/stretchr/testify/suite"
)

type SamaritanoSuite struct {
	suite.Suite

	clusterDir string
	cluster    *cluster.Cluster
	cfg        config.Config
}

func (s *SamaritanoSuite) SetupSuite() {
	dir, err := os.MkdirTemp("", "bootloose-k3s-*")
	s.Require().NoError(err)
	s.clusterDir = dir

	cfg := config.Config{
		Cluster: config.Cluster{
			Name:       "samaritano-test",
			PrivateKey: filepath.Join(dir, "id_rsa"),
		},
		Machines: []config.MachineReplicas{
			{
				Count: 1,
				Spec: &config.Machine{
					Image:      virtualMachineImage,
					Name:       "node%d",
					Privileged: true, // k3s needs this for cgroups/networking
					PortMappings: []config.PortMapping{
						{ContainerPort: 22},                      // SSH (host port assigned dynamically)
						{ContainerPort: 6443},                    // k3s API server
						{ContainerPort: samaritanoApiServerPort}, // samaritano API server
					},
					Volumes: []config.Volume{
						{
							Type:        "volume",
							Destination: "/var/lib/rancher",
						},
						{
							// Mount the samaritano distro binary built by make build-distro
							Type:        "bind",
							Source:      "./tardigrade",
							Destination: "/usr/local/bin/tardigrade",
						},
						{
							// Share the host Docker socket so images can be imported into k3s
							Type:        "bind",
							Source:      "/var/run/docker.sock",
							Destination: "/var/run/docker.sock",
						},
					},
				},
			},
			{
				Count: 1,
				Spec: &config.Machine{
					Image:        "quay.io/k0sproject/bootloose-ubuntu20.04",
					Name:         "worker%d",
					Privileged:   true, // k3s needs this for cgroups/networking
					PortMappings: []config.PortMapping{},
					Volumes: []config.Volume{
						{
							// Mount the samaritano distro binary built by make build-distro
							Type:        "bind",
							Source:      "./tardigrade",
							Destination: "/usr/local/bin/tardigrade",
						},
					},
				},
			},
		},
	}

	s.cfg = cfg

	cl, err := cluster.New(cfg)
	s.Require().NoError(err)
	_ = cl.Delete()
	s.Require().NoError(cl.Create())
	s.cluster = cl
}
func (s *SamaritanoSuite) TearDownSuite() {
	if s.cluster != nil {
		_ = s.cluster.Delete()
	}
	if s.clusterDir != "" {
		_ = os.RemoveAll(s.clusterDir)
	}
}
func (s *SamaritanoSuite) sshToNode(name string) *SSHConn {
	machines, err := s.cluster.Inspect(nil)
	s.Require().NoError(err)

	var m *cluster.Machine
	for _, machine := range machines {
		if machine.Hostname() == name {
			m = machine
			break
		}
	}
	s.Require().NotNilf(m, "machine %q not found", name)

	hostPort, err := m.HostPort(22)
	s.Require().NoError(err)

	// Retry until sshd is up
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var conn *SSHConn
	for {
		conn, err = dial(ctx, "localhost", hostPort, s.cfg.Cluster.PrivateKey)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			s.Require().NoError(fmt.Errorf("SSH never became ready on %s: %w", name, err))
		case <-time.After(time.Second):
		}
	}
	return conn
}

const runtimeConfig = `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: my-cluster
  namespace: default
spec:
  controlPlane:
    samaritano:
      image: samaritano-base:v3
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: NodePort
  upstreamCluster:
    apiServer:
      externalAddress: ""
      sans: []
      extraArgs: {}
    controllerManager:
      extraArgs: {}
    scheduler:
      extraArgs: {}
    storage:
      type: kine
      kine:
        dataSource: ""
`

func (s *SamaritanoSuite) TestProvisionControlPlane() {
	ctx := context.Background()
	t := s.T()

	conn := s.sshToNode("node0")
	defer conn.Close()

	t.Log("waiting for k3s to be ready")
	ctx30, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		out, err := conn.Run(ctx30, "k3s kubectl get nodes")
		if err == nil {
			t.Log(out)
			break
		}
		select {
		case <-ctx30.Done():
			s.Require().NoError(fmt.Errorf("k3s never became ready: %w", err))
		case <-time.After(2 * time.Second):
		}
	}

	t.Log("importing", tardigradeDistroImage, "into k3s containerd")
	out, err := conn.Run(ctx, fmt.Sprintf("docker save %s | k3s ctr images import -", tardigradeDistroImage))
	s.Require().NoError(err, out)
	t.Log(out)

	t.Log("writing config.yaml")
	out, err = conn.Run(ctx, fmt.Sprintf("cat > /root/config.yaml << 'EOF'\n%sEOF", runtimeConfig))
	s.Require().NoError(err, out)

	t.Log("provisioning control plane")
	out, err = conn.Run(ctx, "tardigrade provision controlplane --kubeconfig=/etc/rancher/k3s/k3s.yaml --config=/root/config.yaml --cluster-kubeconfig=/root/kubeconfig.yaml")
	s.Require().NoError(err, out)
	t.Log(out)

	t.Log("waiting for distro deployment pod to be running")
	ctx30, cancel30 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel30()
	for {
		out, err = conn.Run(ctx30, "k3s kubectl get pods -l app.kubernetes.io/name=my-cluster --field-selector=status.phase=Running --no-headers")
		if err == nil && out != "" {
			t.Log(out)
			break
		}
		select {
		case <-ctx30.Done():
			s.Require().NoError(fmt.Errorf("pod for my-cluster never reached Running: %w", err))
		case <-time.After(2 * time.Second):
		}
	}
	time.Sleep(2 * time.Hour)
}
func TestSamaritanoSuite(t *testing.T) {
	suite.Run(t, new(SamaritanoSuite))
}
