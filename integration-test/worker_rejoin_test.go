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

// TestWorkerRejoinAfterRestart verifies that a worker node recovers automatically
// after its heir service is stopped and restarted without manual intervention.
// After the initial join the service is stopped; the test waits until the
// apiserver reports the node as not-ready (confirming the health signal is
// working), then starts the service again and waits for the node to return to
// Ready. A final node count check asserts no duplicate entries were created by
// the rejoin, which would indicate a broken node identity or registration path.
func (s *HeirTestSuite) TestWorkerRejoinAfterRestart() {
	upstreamClusterKubeconfig := "./worker-rejoin-kubeconfig.yaml"
	s.T().Cleanup(func() {
		if err := os.Remove(upstreamClusterKubeconfig); err != nil {
			log.WithError(err).Error("failed to remove upstream cluster kubeconfig")
		}
	})
	clusterName := "test-004"
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
		30086,
		30087,
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

	log.Info("generating join token")
	tokenCmd := exec.Command(heirBinaryPath(),
		"token", "generate",
		"--kubeconfig", upstreamClusterKubeconfig,
		"--expiry", "3h",
	)
	tokenOut, err := tokenCmd.CombinedOutput()
	s.Require().NoError(err, string(tokenOut))
	joinToken := strings.TrimSpace(string(tokenOut))
	log.Info("join token generated")

	log.Info("creating worker node via bootloose")
	workerDir := s.T().TempDir()
	workerCfg, workerCluster, err := provisionWorkerNode("heir-rejoin-worker", "worker%d", workerDir)
	s.Require().NoError(err, "failed to provision worker node")
	s.T().Cleanup(func() { _ = workerCluster.Delete() })
	log.Info("worker node created")

	log.Info("connecting to worker node via SSH")
	workerConn, err := dialBootlooseNode(*workerCfg, workerCluster, "worker0")
	s.Require().NoError(err, "failed to connect to worker node via SSH")
	defer workerConn.Close()

	log.Info("joining worker node to upstream cluster")
	joinOut, err := workerConn.Run(context.Background(), fmt.Sprintf("sudo heir provision worker --token=%s", joinToken))
	s.Require().NoError(err, joinOut)
	log.WithField("output", joinOut).Info("worker node joined successfully")

	statusOut, err := workerConn.Run(context.Background(), "systemctl status heir")
	log.WithField("output", statusOut).Info("heir service status before restart")
	s.Require().NoError(err, statusOut)

	log.Info("waiting for node to become ready in upstream cluster")
	err = waitForNodesReady(upstreamKube, 1, 5*time.Minute)
	s.Require().NoError(err, "node did not become ready before restart")
	log.Info("node is ready")

	log.Info("stopping heir service on worker node")
	_, err = workerConn.Run(context.Background(), "systemctl stop heir")
	s.Require().NoError(err, "failed to stop heir service")
	log.Info("heir service stopped")

	log.Info("waiting for node to be reported as not-ready on apiserver")
	err = waitForNodeNotReady(upstreamKube, 3*time.Minute)
	s.Require().NoError(err, "node did not become not-ready after heir service stopped")
	log.Info("node is not-ready on apiserver")

	log.Info("starting heir service on worker node")
	_, err = workerConn.Run(context.Background(), "systemctl start heir")
	s.Require().NoError(err, "failed to start heir service")
	log.Info("heir service started")

	log.Info("waiting for node to become ready again on apiserver")
	err = waitForNodesReady(upstreamKube, 1, 2*time.Minute)
	s.Require().NoError(err, "node did not become ready after service restart")
	log.Info("node is ready again on apiserver")

	log.Info("asserting no duplicate nodes in upstream cluster")
	ctx := context.Background()
	nodes, err := upstreamKube.ListNodes(ctx)
	s.Require().NoError(err, "failed to list nodes in upstream cluster")
	s.Require().Len(nodes, 1, "expected exactly 1 node after rejoin, found %d", len(nodes))
	log.WithField("node", nodes[0].Name).Info("confirmed single node, no duplicates")
}
