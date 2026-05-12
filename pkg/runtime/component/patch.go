package component

import (
	jsonpatch "github.com/evanphx/json-patch/v5"
	"sigs.k8s.io/yaml"
)

func configPatch(baseYAML []byte, patches string) ([]byte, error) {
	baseJSON, err := yaml.YAMLToJSON(baseYAML)
	if err != nil {
		return nil, err
	}

	current := baseJSON

	patchJSON, err := yaml.YAMLToJSON([]byte(patches))
	if err != nil {
		return nil, err
	}

	// JSON merge patch (RFC 7396): recursively merges objects, replaces arrays.
	current, err = jsonpatch.MergePatch(current, patchJSON)
	if err != nil {
		return nil, err
	}

	return yaml.JSONToYAML(current)
}
