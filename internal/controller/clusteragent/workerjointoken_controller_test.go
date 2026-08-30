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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
)

// testRuntime returns a minimal Runtime value with just enough of Spec.Cluster set for
// mint() to resolve an API server address; it is never persisted, so kubebuilder
// validation markers on the type don't apply here.
func testRuntime() *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			Cluster: controlplanev1alpha1.ClusterSpec{
				ControlPlaneExternalEndpoint: controlplanev1alpha1.ControlPlaneExternalEndpointSpec{
					APIServer: controlplanev1alpha1.ComponentEndpoint{Host: "127.0.0.1", Port: 6443},
				},
			},
		},
	}
}

var _ = Describe("WorkerJoinToken Controller", func() {
	Context("When reconciling a resource with cluster info available", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{Name: resourceName}

		var clientset kubernetes.Interface

		BeforeEach(func() {
			var err error
			clientset, err = kubernetes.NewForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())

			By("creating the custom resource for the Kind WorkerJoinToken")
			workerjointoken := &clusteragentv1alpha1.WorkerJoinToken{}
			err = k8sClient.Get(ctx, typeNamespacedName, workerjointoken)
			if err != nil && errors.IsNotFound(err) {
				resource := &clusteragentv1alpha1.WorkerJoinToken{
					ObjectMeta: metav1.ObjectMeta{
						Name: resourceName,
					},
					Spec: clusteragentv1alpha1.WorkerJoinTokenSpec{
						TTL: metav1.Duration{Duration: time.Hour},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance WorkerJoinToken")
			resource := &clusteragentv1alpha1.WorkerJoinToken{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err != nil {
				if errors.IsNotFound(err) {
					return
				}
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			controllerReconciler := &WorkerJoinTokenReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				Clientset: clientset,
				CA:        []byte("test-ca-data"),
				Runtime:   testRuntime(),
			}
			_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &clusteragentv1alpha1.WorkerJoinToken{}))
			}).Should(BeTrue())
		})

		It("adds a finalizer on the first reconcile, then mints a bootstrap token on the second", func() {
			controllerReconciler := &WorkerJoinTokenReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				Clientset: clientset,
				CA:        []byte("test-ca-data"),
				Runtime:   testRuntime(),
			}

			By("first reconcile: registers the finalizer without minting anything")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &clusteragentv1alpha1.WorkerJoinToken{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(resource, workerJoinTokenFinalizer)).To(BeTrue())

			By("second reconcile: mints a bootstrap token and reports Ready")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			cond := meta.FindStatusCondition(resource.Status.Conditions, typeReadyWorkerJoinToken)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("TokenIssued"))
			Expect(resource.Status.TokenID).NotTo(BeEmpty())
			Expect(resource.Status.SecretRef).NotTo(BeNil())

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resource.Status.SecretRef.Name, Namespace: metav1.NamespaceSystem}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("jointoken"))
		})
	})

	Context("secretDrifted", func() {
		const secretName = "drift-test-jointoken"

		var (
			ctx        context.Context
			reconciler *WorkerJoinTokenReconciler
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = &WorkerJoinTokenReconciler{Client: k8sClient}
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: metav1.NamespaceSystem}})
		})

		It("reports drift when status.secretRef has not been set yet", func() {
			joinToken := &clusteragentv1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test"},
			}

			drifted, err := reconciler.secretDrifted(ctx, joinToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(drifted).To(BeTrue())
		})

		It("reports drift when the referenced secret no longer exists", func() {
			joinToken := &clusteragentv1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test"},
				Status: clusteragentv1alpha1.WorkerJoinTokenStatus{
					SecretRef:      &corev1.LocalObjectReference{Name: secretName},
					SecretChecksum: secretChecksum([]byte("anything")),
				},
			}

			drifted, err := reconciler.secretDrifted(ctx, joinToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(drifted).To(BeTrue())
		})

		It("reports drift when the secret's jointoken content has been tampered with", func() {
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: metav1.NamespaceSystem},
				Data:       map[string][]byte{"jointoken": []byte("tampered-content")},
			})).To(Succeed())

			joinToken := &clusteragentv1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test"},
				Status: clusteragentv1alpha1.WorkerJoinTokenStatus{
					SecretRef:      &corev1.LocalObjectReference{Name: secretName},
					SecretChecksum: secretChecksum([]byte("original-content")),
				},
			}

			drifted, err := reconciler.secretDrifted(ctx, joinToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(drifted).To(BeTrue())
		})

		It("reports no drift when the secret's jointoken content matches the recorded checksum", func() {
			content := []byte("still-correct-content")
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: metav1.NamespaceSystem},
				Data:       map[string][]byte{"jointoken": content},
			})).To(Succeed())

			joinToken := &clusteragentv1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test"},
				Status: clusteragentv1alpha1.WorkerJoinTokenStatus{
					SecretRef:      &corev1.LocalObjectReference{Name: secretName},
					SecretChecksum: secretChecksum(content),
				},
			}

			drifted, err := reconciler.secretDrifted(ctx, joinToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(drifted).To(BeFalse())
		})
	})
})
