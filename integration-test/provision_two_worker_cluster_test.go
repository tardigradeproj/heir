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

func (s *HeirTestSuite) TestProvisionTwoWorkerNodeCluster() {
	upstreamClusterKubeconfig := "./provision-two-worker-cluster-kubeconfig.yaml"
	s.T().Cleanup(func() {
		if err := os.Remove(upstreamClusterKubeconfig); err != nil {
			log.WithError(err).Error("failed to remove upstream cluster kubeconfig")
		}
	})
	clusterName := "test-002"
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
		30082,
		30083,
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
	nodeProfile, err := waitForNodeProfileConfigMap(upstreamKube, 3*time.Minute)
	s.Require().NoError(err, "node-profile configmap not found in upstream cluster")
	log.WithField("node_profile", nodeProfile).Info("node-profile configmap is present")

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

	log.Info("creating worker node 1")
	workerDir1 := s.T().TempDir()
	workerCfg1, workerCluster1, err := provisionWorkerNode("heir-worker-1", "worker%d", workerDir1)
	s.Require().NoError(err, "failed to provision worker node 1")
	s.T().Cleanup(func() { _ = workerCluster1.Delete() })
	log.Info("worker node 1 created")

	log.Info("creating worker node 2")
	workerDir2 := s.T().TempDir()
	workerCfg2, workerCluster2, err := provisionWorkerNode("heir-worker-2", "worker-two%d", workerDir2)
	s.Require().NoError(err, "failed to provision worker node 2")
	s.T().Cleanup(func() { _ = workerCluster2.Delete() })
	log.Info("worker node 2 created")

	log.Info("joining worker node 1")
	workerConn1, err := dialBootlooseNode(*workerCfg1, workerCluster1, "worker0")
	s.Require().NoError(err, "failed to connect to worker node 1 via SSH")
	defer workerConn1.Close()
	joinOut1, err := workerConn1.Run(context.Background(), fmt.Sprintf("sudo heir provision worker --token=%s", joinToken))
	s.Require().NoError(err, joinOut1)
	log.WithField("output", joinOut1).Info("worker node 1 joined successfully")

	statusOut1, err := workerConn1.Run(context.Background(), "systemctl status heir")
	log.WithField("output", statusOut1).Info("worker node 1 heir service status")
	s.Require().NoError(err, statusOut1)

	log.Info("joining worker node 2")
	workerConn2, err := dialBootlooseNode(*workerCfg2, workerCluster2, "worker-two0")
	s.Require().NoError(err, "failed to connect to worker node 2 via SSH")
	defer workerConn2.Close()
	joinOut2, err := workerConn2.Run(context.Background(), fmt.Sprintf("sudo heir provision worker --token=%s", joinToken))
	s.Require().NoError(err, joinOut2)
	log.WithField("output", joinOut2).Info("worker node 2 joined successfully")

	statusOut2, err := workerConn2.Run(context.Background(), "systemctl status heir")
	log.WithField("output", statusOut2).Info("worker node 2 heir service status")
	s.Require().NoError(err, statusOut2)

	log.Info("waiting for both worker nodes to be ready in upstream cluster")
	err = waitForNodesReady(upstreamKube, 2, 10*time.Minute)
	s.Require().NoError(err, "worker nodes did not become ready in upstream cluster")
	log.Info("both worker nodes are ready")
}
