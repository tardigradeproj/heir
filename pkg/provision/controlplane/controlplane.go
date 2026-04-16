package controlplane

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/util/json"
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
