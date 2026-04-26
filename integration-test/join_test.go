package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SamaritanoSuite struct {
	suite.Suite
	clusterDir string
	cluster    *cluster.Cluster
	cfg        config.Config
}

func (s *SamaritanoSuite) SetupSuite() {

	dir, err := os.MkdirTemp("", "bootloose-*")
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
					Image:      "samaritano-integration-test:v1",
					Name:       "bastion%d",
					Privileged: true,
					PortMappings: []config.PortMapping{
						{ContainerPort: 22},
					},
					Networks: []string{"kind"},
					Volumes: []config.Volume{
						{
							// Mount the samaritano distro binary built by make build-distro
							Type:        "bind",
							Source:      "./tardigrade",
							Destination: "/usr/local/bin/tardigrade",
						},
						{
							Type:        "bind",
							Source:      "./bastion-kubeconfig.yaml",
							Destination: "/root/.kube/config",
						},
					},
				},
			},
			{
				Count: 1,
				Spec: &config.Machine{
					Image:      "quay.io/k0sproject/bootloose-ubuntu24.04",
					Name:       "worker%d",
					Privileged: true,
					PortMappings: []config.PortMapping{
						{ContainerPort: 22},
					},
					Networks: []string{"kind"},
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

	s.Require().NoError(err)
	controlPlaneIP, err := controlPlaneNodeIP()
	s.Require().NoError(err)
	s.ConfigureVM(controlPlaneIP)
}
func (s *SamaritanoSuite) TearDownSuite() {
	if s.cluster != nil {
		_ = s.cluster.Delete()
	}
	if s.clusterDir != "" {
		_ = os.RemoveAll(s.clusterDir)
	}
	_ = os.RemoveAll(tardigradeClusterKubeConfigPath)
}
func (s *SamaritanoSuite) ConfigureVM(kindContainerIP string) {
	machines, err := s.cluster.Inspect(nil)
	s.Require().NoError(err)

	for _, machine := range machines {
		func() {
			conn := s.sshToNode(machine.Hostname())
			defer conn.Close()
			out, err := conn.Run(context.Background(), fmt.Sprintf("echo '%s %s' >> /etc/hosts", kindContainerIP, samaritanoExternalAddress))
			s.Require().NoError(err, out)
		}()
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

func (s *SamaritanoSuite) TestProvisionControlPlane() {
	ctx := context.Background()
	t := s.T()

	bastionSSHConnection := s.sshToNode("bastion0")
	defer bastionSSHConnection.Close()
	workerSSHConnection := s.sshToNode("worker0")
	defer workerSSHConnection.Close()

	t.Log("writing tardigrade configuration")
	out, err := bastionSSHConnection.Run(ctx, fmt.Sprintf("cat > /tmp/runtime-config.yaml << 'EOF'\n%sEOF", buildRuntimeConfig()))
	require.NoError(t, err, out)

	t.Log("creating tardigrade cluster")
	out, err = bastionSSHConnection.Run(ctx, "tardigrade provision controlplane"+
		" --kubeconfig /root/.kube/config"+
		" --cluster-kubeconfig /tmp/tardigrade-kubeconfig.yaml"+
		" --config /tmp/runtime-config.yaml")
	require.NoError(t, err, out)
	t.Log(out)

	t.Log("waiting for API server to be ready")
	ctx30, cancel30 := context.WithTimeout(ctx, 90*time.Second)
	defer cancel30()
	for {
		out, err = bastionSSHConnection.Run(ctx30, fmt.Sprintf("curl -kfs https://%s:%d/readyz", samaritanoExternalAddress, samaritanoApiServerPort))
		t.Log(out)
		if err == nil && strings.TrimSpace(out) == "ok" {
			t.Log("API server is ready")
			break
		}
		select {
		case <-ctx30.Done():
			require.NoError(t, fmt.Errorf("API server never became ready: %w", err))
		case <-time.After(2 * time.Second):
		}
	}

	t.Log("generating join command")
	joinScript, err := bastionSSHConnection.Run(ctx, "tardigrade token generate"+
		" --kubeconfig /tmp/tardigrade-kubeconfig.yaml"+
		" --name samaritano-my-cluster@kubernetes"+
		" --expiry 3h")
	require.NoError(t, err, joinScript)

	t.Log(joinScript)
	t.Log("joining worker node")
	joinOut, err := workerSSHConnection.Run(ctx, fmt.Sprintf(`tardigrade provision worker --kubelet-extra-args="fail-swap-on=false" --token=%s`, joinScript))
	require.NoError(t, err, joinOut)
	t.Log(joinOut)

	time.Sleep(2 * time.Hour)
}
func TestSamaritanoSuite(t *testing.T) {
	suite.Run(t, new(SamaritanoSuite))
}
