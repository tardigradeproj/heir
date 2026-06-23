package integration

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"
)

type suiteConfig struct {
	managementClusterKubeconfig string
	managementClusterName       string
	postgresNodePort            uint32
	postgresUsername            string
	postgresPassword            string
	postgresImage               string
	dockerRegistryName          string
	kindRegistryPort            uint32
	apiServerPort               uint32
	konnectivityPort            uint32
	heirSourceImage             string
	heirTargetRef               string
	konnectivitySourceImage     string
	konnectivityTargetRef       string
}

func defaultSuiteConfig() suiteConfig {
	return suiteConfig{
		managementClusterKubeconfig: "./management-cluster.yaml",
		managementClusterName:       "e2e-management-cluster",
		postgresNodePort:            30079,
		postgresUsername:            "user",
		postgresPassword:            "password",
		dockerRegistryName:          "kind-registry",
		kindRegistryPort:            5001,
		heirSourceImage:             fmt.Sprintf("ghcr.io/tardigradeproj/heir:latest-%s", runtime.GOARCH),
		heirTargetRef:               "heir-base:v0.0.1",
		konnectivitySourceImage:     "registry.k8s.io/kas-network-proxy/proxy-server:v0.0.37",
		konnectivityTargetRef:       "proxy-server:v0.0.37",
	}
}

type HeirTestSuite struct {
	suite.Suite
	clusterDir          string
	cluster             *cluster.Cluster
	cfg                 config.Config
	dns                 string
	externalPostgresDSN string
	heirImage           string
	konnectivityImage   string
	managementClusterIP string
	managementKube      *KubeClient
	suiteConfig         suiteConfig
}

func (s *HeirTestSuite) SetupSuite() {
	s.suiteConfig = defaultSuiteConfig()

	log.Info("starting management cluster")
	clusterIP, err := startManagementCluster(s.suiteConfig.managementClusterName, s.suiteConfig.managementClusterKubeconfig,
		[]uint32{s.suiteConfig.postgresNodePort, 30080, 30081, 30082, 30083, 30084, 30085, 30086, 30087})
	s.Require().NoError(err, "failed to start management cluster")
	s.managementClusterIP = clusterIP
	log.WithField("ip", s.managementClusterIP).Info("management cluster node IP assigned")

	log.Info("initializing management cluster client")
	managementKube, err := NewKubeClient(s.suiteConfig.managementClusterKubeconfig)
	s.Require().NoError(err, "failed to create management cluster client")
	s.managementKube = managementKube

	log.Info("waiting for management cluster to become ready")
	err = checkManagementClusterReadiness(s.suiteConfig.managementClusterName, s.managementKube, 80*time.Second)
	s.Require().NoError(err, "management cluster readiness check failed")

	log.Info("starting local registry")
	s.Require().NoError(setupLocalRegistry(s.suiteConfig.dockerRegistryName, s.suiteConfig.kindRegistryPort), "failed to setup local registry")

	log.Info("configuring registry on kind nodes")
	s.Require().NoError(
		configureRegistryOnNodes(s.suiteConfig.managementClusterName, s.suiteConfig.dockerRegistryName, s.suiteConfig.kindRegistryPort),
		"failed setup registry on kind nodes",
	)

	log.Info("applying registry ConfigMap")
	s.Require().NoError(applyRegistryConfigMap(s.managementKube, s.suiteConfig.kindRegistryPort), "failed setup registry configMap")

	log.Info("pushing postgres images to registry")
	postgresImage, err := pushImageToRegistry("postgres:16", "postgres:16", s.suiteConfig.kindRegistryPort)
	s.Require().NoError(err, "failed to push postgres image to registry")

	log.Info("provisioning postgres")
	dsn, err := provisionPostgres(s.managementKube, postgresImage, s.suiteConfig.postgresUsername, s.suiteConfig.postgresPassword, s.suiteConfig.postgresNodePort)
	s.Require().NoError(err, "failed to provision postgres")
	s.dns = dsn
	s.externalPostgresDSN = buildExternalPostgresDSN(s.suiteConfig.postgresUsername, s.suiteConfig.postgresPassword, s.suiteConfig.postgresNodePort)

	log.Info("waiting for postgres to be ready")
	s.Require().NoError(waitForPostgresReady(s.externalPostgresDSN, 2*time.Minute), "postgres did not become ready")

	log.Info("pushing images to registry")
	s.heirImage, err = pushImageToRegistry(s.suiteConfig.heirSourceImage, s.suiteConfig.heirTargetRef, s.suiteConfig.kindRegistryPort)
	s.Require().NoError(err, "failed to push heir image to registry")
	// #TODO: pull first
	s.konnectivityImage, err = pushImageToRegistry(s.suiteConfig.konnectivitySourceImage, s.suiteConfig.konnectivityTargetRef, s.suiteConfig.kindRegistryPort)
	s.Require().NoError(err, "failed to push konnectivity image to registry")

	log.Info("setup complete")
}

func (s *HeirTestSuite) provisionTestDatabase(namespace, clusterName string) string {
	dbSecretName := "postgres-" + clusterName
	_, err := createDatabase(s.externalPostgresDSN, clusterName)
	s.Require().NoError(err, "failed to create test database")
	internalDSN := buildInternalPostgresDSN(s.suiteConfig.postgresUsername, s.suiteConfig.postgresPassword, clusterName)
	err = createKubernetesSecret(s.managementKube, namespace, dbSecretName, map[string]string{"dsn": internalDSN})
	s.Require().NoError(err, "failed to create database secret")
	s.T().Cleanup(func() {
		_ = deleteKubernetesSecret(s.managementKube, namespace, dbSecretName)
	})
	return dbSecretName
}

func (s *HeirTestSuite) TearDownSuite() {
	log.Info("Tearing down Heir suite")
	provider := kindcluster.NewProvider(
		kindcluster.ProviderWithLogger(cmd.NewLogger()),
	)
	s.NoError(provider.Delete(s.suiteConfig.managementClusterName, s.suiteConfig.managementClusterKubeconfig), "failed to delete management cluster")
	s.NoError(os.Remove(s.suiteConfig.managementClusterKubeconfig), "failed to remove management cluster kubeconfig")
	s.NoError(teardownLocalRegistry(s.suiteConfig.dockerRegistryName), "failed to teardown local registry")
}
