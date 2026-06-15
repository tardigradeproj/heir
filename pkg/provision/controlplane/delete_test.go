package controlplane

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDelete(t *testing.T) {
	const (
		clusterName = "test-cluster"
		ns          = "default"
	)

	managedLabels := map[string]string{
		"app.kubernetes.io/name":       clusterName,
		"app.kubernetes.io/managed-by": "heir",
	}

	allResources := func() []k8sruntime.Object {
		return []k8sruntime.Object{
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: ns,
				Labels:      managedLabels,
				Annotations: map[string]string{annotationDeletionProtection: "false"},
			}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: ns,
				Labels:      managedLabels,
				Annotations: map[string]string{annotationDeletionProtection: "false"},
			}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: ns,
				Labels:      managedLabels,
				Annotations: map[string]string{annotationDeletionProtection: "false"},
			}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: ns,
				Labels:      managedLabels,
				Annotations: map[string]string{annotationDeletionProtection: "false"},
			}},
		}
	}

	tests := []struct {
		name       string
		makeClient func() *fake.Clientset
		validate   func(t *testing.T, fc *fake.Clientset, err error)
	}{
		{
			name: "deletes all resources when none are protected",
			makeClient: func() *fake.Clientset {
				return fake.NewClientset(allResources()...)
			},
			validate: func(t *testing.T, fc *fake.Clientset, err error) {
				require.NoError(t, err)
				assert.Equal(t, 4, verbCount(fc, "delete"),
					"expected all 4 resources to be deleted; got: %v", fc.Actions())
			},
		},
		{
			name: "skips resources with deletion-protection: true",
			makeClient: func() *fake.Clientset {
				return fake.NewClientset(
					&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Name: clusterName, Namespace: ns,
						Labels:      managedLabels,
						Annotations: map[string]string{annotationDeletionProtection: "true"},
					}},
					&corev1.Service{ObjectMeta: metav1.ObjectMeta{
						Name: clusterName, Namespace: ns,
						Labels:      managedLabels,
						Annotations: map[string]string{annotationDeletionProtection: "true"},
					}},
					&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
						Name: clusterName, Namespace: ns,
						Labels:      managedLabels,
						Annotations: map[string]string{annotationDeletionProtection: "false"},
					}},
					&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
						Name: clusterName, Namespace: ns,
						Labels:      managedLabels,
						Annotations: map[string]string{annotationDeletionProtection: "false"},
					}},
				)
			},
			validate: func(t *testing.T, fc *fake.Clientset, err error) {
				require.NoError(t, err)
				assert.Equal(t, 2, verbCount(fc, "delete"),
					"deployment and service are protected; only configmap and secret should be deleted; got: %v", fc.Actions())
			},
		},
		{
			name: "secret deletion failure returns error",
			makeClient: func() *fake.Clientset {
				fc := fake.NewClientset(allResources()...)
				fc.Fake.PrependReactor("delete", "secrets",
					func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
						return true, nil, fmt.Errorf("injected: secret delete failed")
					},
				)
				return fc
			},
			validate: func(t *testing.T, _ *fake.Clientset, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), fmt.Sprintf("failed to delete PKI secret storage %s", clusterName))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := tt.makeClient()
			err := Delete(context.Background(),
				withClient(fc),
				WithName(clusterName),
				WithNamespace(ns),
			)
			tt.validate(t, fc, err)
		})
	}
}
