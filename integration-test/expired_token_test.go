package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// TestExpiredJoinToken verifies that the token validation path rejects stale
// credentials. A join token is generated with a 5-second expiry; by the time
// the bootloose worker is provisioned and the join command runs the token has
// expired. The test asserts that no node appears in the upstream cluster,
// confirming that an expired token cannot be used to silently join a node.
func (s *HeirTestSuite) TestExpiredJoinToken() {
	upstreamClusterKubeconfig := "./expired-token-kubeconfig.yaml"
	s.T().Cleanup(func() {
		if err := os.Remove(upstreamClusterKubeconfig); err != nil {
			log.WithError(err).Error("failed to remove upstream cluster kubeconfig")
		}
	})
	clusterName := "test-003"
	namespace := "default"

	log.Info("provisioning database for test")
	dbSecretName := s.provisionTestDatabase(namespace, clusterName)

	log.Info("building runtime config")
	runtimeConfig := buildRuntimeConfig(
		clusterName,
		namespace,
		s.heirImage,
		s.konnectivityImage,
		s.managementClusterIP,
		dbSecretName,
		30084,
		30085,
	)

	log.Info("writing runtime config to temp file")
	configFile, err := os.CreateTemp("", "runtime-config-*.yaml")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.Remove(configFile.Name()) })
	_, err = fmt.Fprint(configFile, runtimeConfig)
	s.Require().NoError(err)
	s.Require().NoError(configFile.Close())

	log.WithFields(log.Fields{
		"cluster": clusterName,
		"binary":  heirBinaryPath(),
		"config":  configFile.Name(),
	}).Info("provisioning control plane")
	provisionCmd := exec.Command(heirBinaryPath(),
		"provision", "controlplane",
		"--kubeconfig", s.suiteConfig.managementClusterKubeconfig,
		"--upstream-kubeconfig", upstreamClusterKubeconfig,
		"--config", configFile.Name(),
		"--use-localhost-context",
	)
	out, err := provisionCmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	log.Info("control plane provisioned")

	log.WithField("cluster", clusterName).Info("waiting for control plane pod to become ready")
	err = waitForControlPlanePodReady(s.managementKube, namespace, clusterName, 180*time.Second)
	s.Require().NoError(err, "control plane pod did not become ready")
	log.Info("control plane pod is ready")

	log.Info("initializing upstream cluster client")
	upstreamKube, err := NewKubeClient(upstreamClusterKubeconfig)
	s.Require().NoError(err, "failed to create upstream cluster client")

	log.Info("waiting for node-profile configmap in upstream cluster")
	_, err = waitForNodeProfileConfigMap(upstreamKube, 3*time.Minute)
	s.Require().NoError(err, "node-profile configmap not found in upstream cluster")

	log.Info("generating short-lived join token (5s expiry)")
	tokenCmd := exec.Command(heirBinaryPath(),
		"token", "generate",
		"--kubeconfig", upstreamClusterKubeconfig,
		"--expiry", "5s",
	)
	tokenOut, err := tokenCmd.CombinedOutput()
	s.Require().NoError(err, string(tokenOut))
	expiredToken := strings.TrimSpace(string(tokenOut))
	log.Info("short-lived token generated, waiting for it to expire")

	// Provision the worker node while the token is expiring; bootloose
	// container creation takes long enough that the 5s token will have expired
	// by the time the join command runs.
	workerDir := s.T().TempDir()
	workerCfg, workerCluster, err := provisionWorkerNode("heir-expired-token-worker", "worker%d", workerDir)
	s.Require().NoError(err, "failed to provision worker node")
	s.T().Cleanup(func() { _ = workerCluster.Delete() })
	log.Info("worker node created")

	// Extra buffer to guarantee the token is expired.
	time.Sleep(10 * time.Second)

	log.Info("connecting to worker node via SSH")
	workerConn, err := dialBootlooseNode(*workerCfg, workerCluster, "worker0")
	s.Require().NoError(err, "failed to connect to worker node via SSH")
	defer workerConn.Close()

	log.WithField("token", expiredToken).Info("attempting to join with expired token — expecting failure")
	joinOut, err := workerConn.Run(context.Background(), fmt.Sprintf("sudo heir provision worker --token=%s", expiredToken))
	log.WithField("output", joinOut).Info("join attempt output")
	time.Sleep(30 * time.Second)
	nodes, err := upstreamKube.ListNodes(context.Background())
	s.Require().NoError(err, "failed to list nodes in upstream cluster")
	s.Require().Len(nodes, 0, "expected exactly 0 node after joining with expired token, found %d", len(nodes))

	log.Info("join correctly rejected expired token")
}
