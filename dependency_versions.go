package heir

import (
	_ "embed"
	"fmt"

	"sigs.k8s.io/yaml"
)

//go:embed .dependencies-version.yaml
var dependencyVersionsYAML []byte

type DependencyVersions struct {
	CorednsChart string `json:"coredns-chart"`
	FlannelChart string `json:"flannel-chart"`
}

// Dependencies is the parsed contents of the embedded .dependencies-version.yaml.
var Dependencies = mustParseDependencyVersions(dependencyVersionsYAML)

func mustParseDependencyVersions(data []byte) DependencyVersions {
	var v DependencyVersions
	if err := yaml.Unmarshal(data, &v); err != nil {
		panic(fmt.Sprintf("heir: failed to parse embedded .dependencies-version.yaml: %v", err))
	}
	return v
}
