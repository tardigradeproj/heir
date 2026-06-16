package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
)

const (
	managementClusterKubeconfig = "./management-cluster.yaml"
	managementClusterName       = "e2e-management-cluster"
	postgresNodePort            = 30079
	postgresUsername            = "user"
	postgresPassword            = "password"
	postgresImage               = "postgres:16"
	DockerRegistryName          = "kind-registry"
)

type HeirTestSuite struct {
	suite.Suite
	clusterDir string
	cluster    *cluster.Cluster
	cfg        config.Config
}

func (s *HeirTestSuite) SetupSuite() {

	log.Info("starting management cluster")
	err := startManagementCluster(managementClusterName, managementClusterKubeconfig, []uint32{postgresNodePort, 30080, 30081, 30082, 30083})
	s.Require().NoError(err, "failed to start management cluster")

	log.Info("waiting for management cluster to become ready")
	readinessCheckDuration := 80 * time.Second
	err = checkManagementClusterReadiness(managementClusterName, managementClusterKubeconfig, readinessCheckDuration)
	s.Require().NoError(err, "management cluster readiness check failed")

	log.Info("provisioning postgres")
	err = provisionPostgres(managementClusterKubeconfig, postgresImage, postgresUsername, postgresPassword, postgresNodePort)
	s.Require().NoError(err, "failed to provision postgres")

	var registryPort uint32 = 5001
	log.Info("starting local registry")
	s.Require().NoError(setupLocalRegistry(DockerRegistryName, registryPort), "failed to setup local registry")

	log.Info("configuring registry on kind nodes")
	s.Require().NoError(
		configureRegistryOnNodes(managementClusterName, DockerRegistryName, registryPort),
		"failed setup registry on kind nodes",
	)

	log.Info("applying registry ConfigMap")
	s.Require().NoError(applyRegistryConfigMap(managementClusterKubeconfig, registryPort), "failed setup registry configMap")

	log.Info("pushing image to registry")
	s.Require().NoError(
		pushImageToRegistry("ghcr.io/tardigradeproj/heir:v1.35.5-heir0", "heir-base:v0.0.1", registryPort),
		"failed to push image to registry",
	)
	log.Info("setup complete")
}

func (s *HeirTestSuite) TearDownSuite() {
	log.Info("Tearing down Heir suite")
	provider := kindcluster.NewProvider(
		kindcluster.ProviderWithLogger(cmd.NewLogger()),
	)
	s.NoError(provider.Delete(managementClusterName, managementClusterKubeconfig), "failed to delete management cluster")
	s.NoError(os.Remove(managementClusterKubeconfig), "failed to remove management cluster kubeconfig")
	s.NoError(teardownLocalRegistry(DockerRegistryName), "failed to teardown local registry")
}
func (s *HeirTestSuite) TestApplication() {
	//time.Sleep(1 * time.Hour)
	fmt.Println("==>")
}
func TestHeirSuite(t *testing.T) {
	suite.Run(t, new(HeirTestSuite))
}
