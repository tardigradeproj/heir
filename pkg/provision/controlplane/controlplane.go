package controlplane

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	samaritanoruntime "github.com/tardigrade-runtime/samaritano/pkg/runtime"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

//go:embed controlplane.tardigrade.runtime.io_runtimes.yaml
var crdManifest []byte

func Provision(ctx context.Context, opts ...Option) error {
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

	client, err := buildClient(pCtx.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build kubernetes client: %w", err)
	}

	layout := samaritanoruntime.NewControlPlaneLayout()

	if err := setupPKIAuth(ctx, client, runtime, layout); err != nil {
		return fmt.Errorf("failed to setup PKI: %w", err)
	}

	configHash, err := setupConfig(ctx, client, runtime, layout)
	if err != nil {
		return fmt.Errorf("failed to setup config: %w", err)
	}

	if err := setupService(ctx, client, runtime); err != nil {
		return fmt.Errorf("failed to setup service: %w", err)
	}

	if err := setupDeployment(ctx, client, runtime, layout, configHash); err != nil {
		return fmt.Errorf("failed to setup deployment: %w", err)
	}

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

func setupPKIAuth(ctx context.Context, client *kubernetes.Clientset, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout) error {
	secret, err := samaritanoruntime.GeneratePKIAuthSecret(runtime, layout)
	if err != nil {
		return err
	}
	log.WithField("secret", secret.Name).Info("creating PKI auth secret")
	if _, err := client.CoreV1().Secrets(runtime.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create PKI auth secret: %w", err)
	}
	log.Info("PKI auth secret created")

	return nil
}

func setupConfig(ctx context.Context, client *kubernetes.Clientset, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout) (string, error) {
	fmt.Println("1==>")
	cm, configHash, err := samaritanoruntime.GenerateControlPlaneConfig(runtime, layout)
	if err != nil {
		return "", err
	}
	fmt.Println("==>")
	log.WithField("configmap", cm.Name).Info("creating config configmap")
	if _, err := client.CoreV1().ConfigMaps(runtime.Namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("failed to create config configmap: %w", err)
	}
	log.Info("config configmap created")
	return configHash, nil
}

func setupService(ctx context.Context, client *kubernetes.Clientset, runtime *v1alpha1.Runtime) error {
	svc, err := samaritanoruntime.GenerateService(runtime)
	if err != nil {
		return err
	}
	log.WithField("service", svc.Name).Info("creating service")
	if _, err := client.CoreV1().Services(runtime.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	log.Info("service created")
	return nil
}

func setupDeployment(ctx context.Context, client *kubernetes.Clientset, runtime *v1alpha1.Runtime, layout samaritanoruntime.ControlPlaneLayout, configHash string) error {
	deploy, err := samaritanoruntime.GenerateDeployment(runtime, layout, configHash)
	if err != nil {
		return err
	}
	log.WithField("deployment", deploy.Name).Info("creating deployment")
	if _, err := client.AppsV1().Deployments(runtime.Namespace).Create(ctx, deploy, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	log.Info("deployment created")
	return nil
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
