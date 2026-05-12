package controlplane

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	samaritanoruntime "github.com/tardigrade-runtime/samaritano/pkg/runtime"
	"gvisor.dev/gvisor/pkg/cleanup"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"
)

//go:embed controlplane.tardigrade.runtime.io_runtimes.yaml
var crdManifest []byte

func Provision(ctx context.Context, opts ...Option) error {
	cleaner := cleanup.Make(func() {})
	defer cleaner.Clean()
	pCtx := &provisionContext{}
	for _, opt := range opts {
		opt(pCtx)
	}

	runtime, err := parseConfig(pCtx.config)
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	if pCtx.name != "" {
		runtime.Name = pCtx.name
	}
	if pCtx.namespace != "" {
		runtime.Namespace = pCtx.namespace
	}

	log.WithFields(log.Fields{
		"cluster.name": runtime.Name,
		"namespace":    runtime.Namespace,
	}).Info("provisioning control plane")

	var client kubernetes.Interface
	if pCtx.client != nil {
		client = pCtx.client
	} else {
		cs, err := buildClient(pCtx.kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to build kubernetes client: %w", err)
		}
		client = cs
	}

	layout := samaritanoruntime.NewControlPlaneLayout()

	kubeconfig, err := setupPKIAuth(ctx, &cleaner, client, runtime, layout)
	if err != nil {
		return fmt.Errorf("failed to setup PKI auth: %w", err)
	}

	if err := setupStorageSecret(ctx, &cleaner, client, runtime, layout); err != nil {
		return fmt.Errorf("failed to setup storage secret: %w", err)
	}

	configHash, err := setupConfig(ctx, &cleaner, client, runtime, layout)
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	if err := setupService(ctx, &cleaner, client, runtime); err != nil {
		return fmt.Errorf("failed to setup service: %w", err)
	}

	if err := setupDeployment(ctx, &cleaner, client, runtime, layout, configHash); err != nil {
		return fmt.Errorf("failed to setup deployment: %w", err)
	}
	if pCtx.clusterKubeconfig != "" {
		if err := writeKubeconfig(kubeconfig, pCtx.clusterKubeconfig, runtime.Spec.UpstreamCluster.APIServer.ExternalAddresses); err != nil {
			return fmt.Errorf("failed to write kubeconfig to %s: %w", pCtx.clusterKubeconfig, err)
		}
		log.WithField("path", pCtx.clusterKubeconfig).Info("kubeconfig written")
	}
	cleaner.Release()
	return nil
}

func buildClient(kubeconfig string) (*kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

func setupPKIAuth(ctx context.Context,
	cleaner *cleanup.Cleanup,
	client kubernetes.Interface,
	runtime *v1alpha1.Runtime,
	layout samaritanoruntime.ControlPlaneLayout,
) (*clientcmdapi.Config, error) {
	secret, err := samaritanoruntime.GeneratePKIAuthSecret(runtime, layout)
	if err != nil {
		return nil, err
	}
	log.WithField("secret", secret.Name).Info("creating PKI auth secret")
	if _, err := client.CoreV1().Secrets(runtime.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("failed to create PKI auth secret: %w", err)
	}
	cleaner.Add(func() {
		if err := client.CoreV1().Secrets(runtime.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil {
			log.WithError(err).WithField("ops", "cleanup").Error("failed to delete PKI auth secret")
		}
	})
	log.Info("PKI auth secret created")
	kubeconfigBytes, ok := secret.Data[layout.Auth.AdminConf.SecretKey]
	if !ok {
		return nil, fmt.Errorf("PKI auth secret does not contain admin kubeconfig")
	}
	kubeconfig, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse admin kubeconfig: %w", err)
	}
	return kubeconfig, nil
}

func setupStorageSecret(ctx context.Context, cleaner *cleanup.Cleanup, client kubernetes.Interface, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout) error {
	var dataSource string
	if ref := runtime.Spec.UpstreamCluster.Storage.Kine.DataSourceRef; ref != nil {
		ds, err := client.CoreV1().Secrets(runtime.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to resolve storage DataSourceRef %s/%s: %w", runtime.Namespace, ref.Name, err)
		}
		dataSource = string(ds.Data[ref.Key])
	}
	secret := samaritanoruntime.GenerateStorageSecret(runtime, layout, dataSource)
	log.WithField("secret", secret.Name).Info("creating storage secret")
	if _, err := client.CoreV1().Secrets(runtime.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create storage secret: %w", err)
	}
	cleaner.Add(func() {
		if err := client.CoreV1().Secrets(runtime.Namespace).Delete(ctx, secret.Name, metav1.DeleteOptions{}); err != nil {
			log.WithError(err).WithField("ops", "cleanup").Error("failed to delete storage secret")
		}
	})
	log.Info("storage secret created")
	return nil
}

func setupConfig(ctx context.Context, cleaner *cleanup.Cleanup, client kubernetes.Interface, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout) (string, error) {
	cm, configHash, err := samaritanoruntime.GenerateControlPlaneConfig(runtime, layout)
	if err != nil {
		return "", err
	}
	log.WithField("configmap", cm.Name).Info("creating config configmap")
	if _, err := client.CoreV1().ConfigMaps(runtime.Namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("failed to create config configmap: %w", err)
	}
	cleaner.Add(func() {
		if err := client.CoreV1().ConfigMaps(runtime.Namespace).Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil {
			log.WithError(err).WithField("ops", "cleanup").Error("failed to delete configuration configmap")
		}
	})
	log.Info("config configmap created")
	return configHash, nil
}

func setupService(ctx context.Context, cleaner *cleanup.Cleanup, client kubernetes.Interface, runtime *v1alpha1.Runtime) error {
	svc, err := samaritanoruntime.GenerateService(runtime)
	if err != nil {
		return err
	}
	log.WithField("service", svc.Name).Info("creating service")
	if _, err := client.CoreV1().Services(runtime.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	cleaner.Add(func() {
		if err := client.CoreV1().Services(runtime.Namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{}); err != nil {
			log.WithError(err).WithField("ops", "cleanup").Error("failed to delete service")
		}
	})
	log.Info("service created")
	return nil
}

func setupDeployment(ctx context.Context, cleaner *cleanup.Cleanup, client kubernetes.Interface, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout, configHash string) error {
	deploy, err := samaritanoruntime.GenerateDeployment(runtime, layout, configHash)
	if err != nil {
		return err
	}
	log.WithField("deployment", deploy.Name).Info("creating deployment")
	if _, err := client.AppsV1().Deployments(runtime.Namespace).Create(ctx, deploy, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	cleaner.Add(func() {
		if err := client.AppsV1().Deployments(runtime.Namespace).Delete(ctx, deploy.Name, metav1.DeleteOptions{}); err != nil {
			log.WithError(err).WithField("ops", "cleanup").Error("failed to delete deployment")
		}
	})
	log.Info("deployment created")
	return nil
}

// writeKubeconfig writes the given kubeconfig to path. If a valid kubeconfig
// already exists at that path, the new clusters, users, and contexts are merged
// into it and the current context is updated to the new one.
func writeKubeconfig(kubeconfig *clientcmdapi.Config, path string, clusterExternalUrls []string) error {
	if len(clusterExternalUrls) == 0 {
		log.Warn("the generated kubeconfig does not contain a server address and cannot be used to communicate with the Kubernetes API server; " +
			"to resolve this, set spec.upstreamCluster.apiServer.externalAddresses to the externally reachable address of the control plane " +
			"(e.g. [https://my-cluster.example.com:6443]) and re-provision")
	} else {
		// Expand the single generated cluster entry into one entry per external URL.
		// The first URL keeps the original cluster name; subsequent URLs get a numeric suffix.
		expanded := make(map[string]*clientcmdapi.Cluster, len(clusterExternalUrls))
		for origName, origCluster := range kubeconfig.Clusters {
			for i, url := range clusterExternalUrls {
				name := origName
				if i > 0 {
					name = fmt.Sprintf("%s-%d", origName, i+1)
				}
				c := *origCluster
				c.Server = url
				expanded[name] = &c
			}
		}
		kubeconfig.Clusters = expanded
	}
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load existing kubeconfig: %w", err)
	}
	if os.IsNotExist(err) {
		return clientcmd.WriteToFile(*kubeconfig, path)
	}
	for k, v := range kubeconfig.Clusters {
		existing.Clusters[k] = v
	}
	for k, v := range kubeconfig.AuthInfos {
		existing.AuthInfos[k] = v
	}
	for k, v := range kubeconfig.Contexts {
		existing.Contexts[k] = v
	}
	existing.CurrentContext = kubeconfig.CurrentContext
	return clientcmd.WriteToFile(*existing, path)
}

// parseConfig reads a Runtime manifest from path, applies CRD-derived defaults,
// and returns the populated Runtime object.
func parseConfig(path string) (*v1alpha1.Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read samaritano config file: %w", err)
	}

	// Unmarshal into a raw map so we can run the structural schema defaulter
	// before binding to the typed struct.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	ss, err := structuralSchema()
	if err != nil {
		return nil, fmt.Errorf("that issue is with samaritano itself, please contact the maintainers. Failed to load CRD schema: %w", err)
	}
	// Apply the same OpenAPI defaulting the API server runs on admission.
	defaulting.Default(raw, ss)

	// Round-trip through JSON to bind into the typed struct.
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encode defaulted object: %w", err)
	}

	var r v1alpha1.Runtime
	if err := json.Unmarshal(jsonBytes, &r); err != nil {
		return nil, fmt.Errorf("failed to parse configuration file: %w", err)
	}

	return &r, nil
}

// structuralSchema extracts the v1alpha1 structural schema from the embedded CRD manifest.
func structuralSchema() (*schema.Structural, error) {
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(crdManifest, &crd); err != nil {
		return nil, fmt.Errorf("failed to parse embedded CRD: %w", err)
	}

	var v1Props *apiextensionsv1.JSONSchemaProps
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == "v1alpha1" {
			v1Props = crd.Spec.Versions[i].Schema.OpenAPIV3Schema
			break
		}
	}
	if v1Props == nil {
		return nil, fmt.Errorf("v1alpha1 schema not found in embedded CRD")
	}

	// schema.NewStructural expects the internal (unversioned) type.
	// The internal type has no JSON tags, so a JSON round-trip silently drops
	// all fields. Use the generated conversion function instead.
	var internalProps apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		v1Props, &internalProps, nil,
	); err != nil {
		return nil, fmt.Errorf("failed to convert v1 schema to internal: %w", err)
	}

	ss, err := schema.NewStructural(&internalProps)
	if err != nil {
		return nil, fmt.Errorf("failed to build structural schema: %w", err)
	}

	return ss, nil
}
