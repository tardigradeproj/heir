/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusteragent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/avast/retry-go"
	log "github.com/sirupsen/logrus"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
)

// DefaultCRDDir is where images/Dockerfile.clusteragent bakes this cluster's CRD YAML
// files into the image.
const DefaultCRDDir = "/etc/kubernetes/crd"

// ApplyCRDs reads every CRD YAML file in dir and creates or updates the corresponding
// CustomResourceDefinition against the cluster cfg points at.
// Each CRD is applied with its own exponential backoff, since the API server may not be
// reachable yet this early in startup.
func ApplyCRDs(ctx context.Context, cfg *rest.Config, dir string) error {

	crds, err := readCRDFiles(dir)
	if err != nil {
		return err
	}

	client, err := apiextensionsclientset.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to build apiextensions client: %w", err)
	}

	for _, crd := range crds {
		if err := applyCRDWithBackoff(ctx, client, crd); err != nil {
			log.WithField("crd", crd.Name).Error(err, "failed to apply CRD")
			return fmt.Errorf("failed to apply CRD %q: %w", crd.Name, err)
		}
		log.WithField("crd", crd.Name).Info("applied CRD")
	}
	return nil
}

// readCRDFiles reads every .yaml/.yml file in dir, in sorted order, and parses each as
// one or more CustomResourceDefinition documents.
func readCRDFiles(dir string) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read CRD directory %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yaml" && ext != ".yml" {
			log.WithField("extension", ext).Debug("invalid CRD extension")
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var crds []*apiextensionsv1.CustomResourceDefinition
	for _, name := range names {
		path := filepath.Join(dir, name)
		docs, err := readCRDDocuments(path)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %q: %w", path, err)
		}
		crds = append(crds, docs...)
	}
	return crds, nil
}

// readCRDDocuments opens path and decodes every YAML document in it as a
// CustomResourceDefinition, skipping empty documents. A file normally holds exactly one
// CRD, but this also tolerates multi-document files.
func readCRDDocuments(path string) ([]*apiextensionsv1.CustomResourceDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(f, 4096)
	var crds []*apiextensionsv1.CustomResourceDefinition
	for {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := decoder.Decode(crd); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if crd.Name == "" {
			continue
		}
		crds = append(crds, crd)
	}
	return crds, nil
}

// applyCRDWithBackoff creates crd if it doesn't exist yet, or updates it if its spec has
// drifted, retrying transient failures with exponential backoff.
func applyCRDWithBackoff(ctx context.Context, client apiextensionsclientset.Interface, crd *apiextensionsv1.CustomResourceDefinition) error {
	return retry.Do(
		func() error { return applyCRD(ctx, client, crd) },
		retry.Context(ctx),
		retry.Attempts(4),
		retry.Delay(time.Second),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(attempt uint, err error) {
			log.WithError(err).
				WithField("attempt", attempt).
				WithField("crd", crd.Name).
				Error(err, "failed to apply CRD, retrying")
		}),
	)
}

func applyCRD(ctx context.Context, client apiextensionsclientset.Interface, desired *apiextensionsv1.CustomResourceDefinition) error {
	crds := client.ApiextensionsV1().CustomResourceDefinitions()

	existing, err := crds.Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := crds.Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return nil
	}
	existing.Spec = desired.Spec
	_, err = crds.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}
