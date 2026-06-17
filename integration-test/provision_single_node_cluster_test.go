package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

func (s *HeirTestSuite) TestProvisionSingleWokerNodeCluster() {
	upstreamClusterKubeconfig := "./provision-single-worker-cluster-kubeconfig.yaml"
	s.T().Cleanup(func() {
		if err := os.Remove(upstreamClusterKubeconfig); err != nil {
			log.WithError(err).Error("failed to remove upstream cluster kubeconfig")
		}
	})
	clusterName := "test-001"
	namespace := "default"
	//
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
		30080,
		30081,
	)

	log.Info("writing runtime config to temp file")
	configFile, err := os.CreateTemp("", "runtime-config-*.yaml")
	s.Require().NoError(err)
	s.T().Cleanup(func() {
		_ = os.Remove(configFile.Name())
	})
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

	log.Info("creating worker node via bootloose")
	workerDir := s.T().TempDir()
	workerCfg, workerCluster, err := provisionWorkerNode("worker01", "worker01%d", workerDir)
	s.Require().NoError(err, "failed to provision worker node")
	s.T().Cleanup(func() { _ = workerCluster.Delete() })
	log.Info("worker node created")

	log.Info("connecting to worker node via SSH")
	workerConn, err := dialBootlooseNode(*workerCfg, workerCluster, "worker01")
	s.Require().NoError(err, "failed to connect to worker node via SSH")
	defer workerConn.Close()

	log.Info("joining worker node to upstream cluster")
	joinOut, err := workerConn.Run(context.Background(), fmt.Sprintf("sudo heir provision worker --token=%s", joinToken))
	s.Require().NoError(err, joinOut)
	log.WithField("output", joinOut).Info("worker node joined successfully")

	log.Info("checking heir service status on worker node")
	statusOut, err := workerConn.Run(context.Background(), "systemctl status heir")
	log.WithField("output", statusOut).Info("heir service status")
	s.Require().NoError(err, statusOut)

	log.Info("waiting for worker node to be ready in upstream cluster")
	err = waitForNodeReady(upstreamKube, 10*time.Minute)
	s.Require().NoError(err, "worker node did not become ready in upstream cluster")
	log.Info("worker node is ready")
}

func TestHeirSuite(t *testing.T) {
	suite.Run(t, new(HeirTestSuite))
}
