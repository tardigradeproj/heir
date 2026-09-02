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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

// writeFile writes content to dir/name and returns the full path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// testCRD builds a minimal, valid CustomResourceDefinition for use as test fixture data.
func testCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Foo",
				ListKind: "FooList",
				Plural:   "foos",
				Singular: "foo",
			},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true},
			},
		},
	}
}

func testCRDYAML(t *testing.T, name string) string {
	t.Helper()
	data, err := yaml.Marshal(testCRD(name))
	require.NoError(t, err)
	return string(data)
}

// ---- readCRDDocuments ----

func TestReadCRDDocuments(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		missingFile bool
		wantErr     bool
		validate    func(t *testing.T, docs []*apiextensionsv1.CustomResourceDefinition)
	}{
		{
			name:    "single CRD document is parsed",
			content: testCRDYAML(t, "foos.example.com"),
			validate: func(t *testing.T, docs []*apiextensionsv1.CustomResourceDefinition) {
				require.Len(t, docs, 1)
				assert.Equal(t, "foos.example.com", docs[0].Name)
			},
		},
		{
			name:    "multi-document file parses every CRD",
			content: testCRDYAML(t, "foos.example.com") + "\n---\n" + testCRDYAML(t, "bars.example.com"),
			validate: func(t *testing.T, docs []*apiextensionsv1.CustomResourceDefinition) {
				require.Len(t, docs, 2)
				assert.Equal(t, "foos.example.com", docs[0].Name)
				assert.Equal(t, "bars.example.com", docs[1].Name)
			},
		},
		{
			name:    "empty documents between separators are skipped",
			content: "---\n" + testCRDYAML(t, "foos.example.com") + "\n---\n---\n",
			validate: func(t *testing.T, docs []*apiextensionsv1.CustomResourceDefinition) {
				require.Len(t, docs, 1)
				assert.Equal(t, "foos.example.com", docs[0].Name)
			},
		},
		{
			name:    "malformed yaml returns an error",
			content: "this: [is not, valid yaml",
			wantErr: true,
		},
		{
			name:        "nonexistent file returns an error",
			missingFile: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.missingFile {
				path = filepath.Join(t.TempDir(), "does-not-exist.yaml")
			} else {
				path = writeFile(t, t.TempDir(), "crd.yaml", tt.content)
			}

			docs, err := readCRDDocuments(path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.validate(t, docs)
		})
	}
}

// ---- readCRDFiles ----

func TestReadCRDFiles(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		wantErr  bool
		errCon   string
		validate func(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition)
	}{
		{
			name: "reads every .yaml/.yml file in sorted order, ignoring non-yaml entries and subdirectories",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "b.yaml", testCRDYAML(t, "b.example.com"))
				writeFile(t, dir, "a.yml", testCRDYAML(t, "a.example.com"))
				writeFile(t, dir, "README.md", "not a CRD")
				require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0o755))
			},
			validate: func(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition) {
				require.Len(t, crds, 2)
				assert.Equal(t, "a.example.com", crds[0].Name, "a.yml sorts before b.yaml")
				assert.Equal(t, "b.example.com", crds[1].Name)
			},
		},
		{
			name: "empty directory yields no CRDs",
			setup: func(t *testing.T, dir string) {
			},
			validate: func(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition) {
				assert.Empty(t, crds)
			},
		},
		{
			name: "malformed yaml in one file fails the whole read, naming the offending file",
			setup: func(t *testing.T, dir string) {
				writeFile(t, dir, "good.yaml", testCRDYAML(t, "good.example.com"))
				writeFile(t, dir, "bad.yaml", "this: [is not, valid yaml")
			},
			wantErr: true,
			errCon:  "bad.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			crds, err := readCRDFiles(dir)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errCon != "" {
					assert.Contains(t, err.Error(), tt.errCon)
				}
				return
			}
			require.NoError(t, err)
			tt.validate(t, crds)
		})
	}

	t.Run("nonexistent directory returns an error", func(t *testing.T) {
		_, err := readCRDFiles(filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
	})
}

// ---- applyCRD ----

func countActions(fc *fake.Clientset, verb string) int {
	n := 0
	for _, a := range fc.Actions() {
		if a.GetVerb() == verb && a.GetResource().Resource == "customresourcedefinitions" {
			n++
		}
	}
	return n
}

func updatedCRD(fc *fake.Clientset) *apiextensionsv1.CustomResourceDefinition {
	for _, a := range fc.Actions() {
		if a.GetVerb() == "update" {
			return a.(k8stesting.UpdateAction).GetObject().(*apiextensionsv1.CustomResourceDefinition)
		}
	}
	return nil
}

func TestApplyCRD(t *testing.T) {
	driftedExisting := testCRD("foos.example.com")
	driftedExisting.Spec.Group = "old.example.com"
	driftedExisting.ResourceVersion = "42"

	tests := []struct {
		name      string
		desired   *apiextensionsv1.CustomResourceDefinition
		existing  []runtime.Object
		reactor   k8stesting.ReactionFunc
		wantErr   bool
		wantCreat int
		wantUpd   int
		validate  func(t *testing.T, fc *fake.Clientset)
	}{
		{
			name:      "creates the CRD when it doesn't exist yet",
			desired:   testCRD("foos.example.com"),
			wantCreat: 1,
		},
		{
			name:     "does nothing when the existing spec already matches",
			desired:  testCRD("foos.example.com"),
			existing: []runtime.Object{testCRD("foos.example.com")},
			wantUpd:  0,
		},
		{
			name:     "updates the CRD when the spec has drifted",
			desired:  testCRD("foos.example.com"),
			existing: []runtime.Object{driftedExisting},
			wantUpd:  1,
			validate: func(t *testing.T, fc *fake.Clientset) {
				updated := updatedCRD(fc)
				require.NotNil(t, updated)
				assert.Equal(t, "example.com", updated.Spec.Group, "spec must be replaced with the desired one")
				assert.Equal(t, "42", updated.ResourceVersion, "must update the fetched object, preserving resourceVersion")
			},
		},
		{
			name:    "a Get error other than NotFound is surfaced",
			desired: testCRD("foos.example.com"),
			reactor: func(action k8stesting.Action) (bool, runtime.Object, error) {
				if action.GetVerb() == "get" {
					return true, nil, assert.AnError
				}
				return false, nil, nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fake.NewSimpleClientset(tt.existing...)
			if tt.reactor != nil {
				fc.Fake.PrependReactor("*", "customresourcedefinitions", tt.reactor)
			}

			err := applyCRD(context.Background(), fc, tt.desired)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCreat, countActions(fc, "create"))
			assert.Equal(t, tt.wantUpd, countActions(fc, "update"))
			if tt.validate != nil {
				tt.validate(t, fc)
			}
		})
	}
}

// ---- applyCRDWithBackoff ----

func TestApplyCRDWithBackoff(t *testing.T) {
	t.Run("succeeds on the first attempt with no retries", func(t *testing.T) {
		fc := fake.NewSimpleClientset()

		err := applyCRDWithBackoff(context.Background(), fc, testCRD("foos.example.com"))

		require.NoError(t, err)
		assert.Equal(t, 1, countActions(fc, "create"))
	})

	t.Run("retries a transient failure and succeeds", func(t *testing.T) {
		fc := fake.NewSimpleClientset()
		var failedOnce bool
		fc.Fake.PrependReactor("create", "customresourcedefinitions", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if !failedOnce {
				failedOnce = true
				return true, nil, assert.AnError
			}
			return false, nil, nil
		})

		err := applyCRDWithBackoff(context.Background(), fc, testCRD("foos.example.com"))

		require.NoError(t, err)
		assert.Equal(t, 2, countActions(fc, "create"), "first attempt fails, second succeeds")
	})

	t.Run("gives up once the context is done, without waiting out the full backoff budget", func(t *testing.T) {
		fc := fake.NewSimpleClientset()
		fc.Fake.PrependReactor("create", "customresourcedefinitions", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, assert.AnError
		})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := applyCRDWithBackoff(ctx, fc, testCRD("foos.example.com"))
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Less(t, elapsed, 5*time.Second, "context cancellation should cut retries short well before the configured backoff ceiling")
	})
}
