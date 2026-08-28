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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
)

var _ = Describe("WorkerJoinToken Controller", func() {
	Context("When reconciling a resource whose Runtime is not ready", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind WorkerJoinToken")
			workerjointoken := &controlplanev1alpha1.WorkerJoinToken{}
			err := k8sClient.Get(ctx, typeNamespacedName, workerjointoken)
			if err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.WorkerJoinToken{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: controlplanev1alpha1.WorkerJoinTokenSpec{
						RuntimeRef: corev1.LocalObjectReference{Name: "nonexistent-runtime"},
						TTL:        metav1.Duration{Duration: time.Hour},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the specific resource instance WorkerJoinToken")
			resource := &controlplanev1alpha1.WorkerJoinToken{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err != nil {
				if errors.IsNotFound(err) {
					return
				}
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			controllerReconciler := &WorkerJoinTokenReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &controlplanev1alpha1.WorkerJoinToken{}))
			}).Should(BeTrue())
		})

		It("adds a finalizer on the first reconcile, then reports Degraded because the referenced Runtime doesn't exist", func() {
			controllerReconciler := &WorkerJoinTokenReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("first reconcile: registers the finalizer without minting anything")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			resource := &controlplanev1alpha1.WorkerJoinToken{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(resource, workerJoinTokenFinalizer)).To(BeTrue())

			By("second reconcile: fails to resolve the Runtime and reports Degraded")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("RuntimeNotReady"))

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			cond := meta.FindStatusCondition(resource.Status.Conditions, typeReadyWorkerJoinToken)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("RuntimeNotReady"))
			Expect(resource.Status.TokenID).To(BeEmpty(), "no token should have been minted")
		})
	})

	Context("When the referenced Runtime exists but has no admin kubeconfig secret yet", func() {
		const (
			resourceName = "test-resource-pending-kubeconfig"
			runtimeName  = "test-runtime-pending-kubeconfig"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}
		runtimeNamespacedName := types.NamespacedName{Name: runtimeName, Namespace: "default"}

		BeforeEach(func() {
			By("creating a minimal Runtime with no admin kubeconfig secret")
			runtime := &controlplanev1alpha1.Runtime{}
			if err := k8sClient.Get(ctx, runtimeNamespacedName, runtime); err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{
						Name:      runtimeName,
						Namespace: "default",
					},
					Spec: controlplanev1alpha1.RuntimeSpec{
						ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
							Heir:    controlplanev1alpha1.HeirSpec{Image: "registry.example.com/heir:latest"},
							Service: controlplanev1alpha1.ServiceSpec{ServiceType: corev1.ServiceTypeClusterIP},
						},
						Cluster: controlplanev1alpha1.ClusterSpec{
							ControlPlaneExternalEndpoint: controlplanev1alpha1.ControlPlaneExternalEndpointSpec{
								PlaneTunnel: controlplanev1alpha1.ComponentEndpoint{
									Port: 8080,
									Host: "127.0.0.1",
								},
								APIServer: controlplanev1alpha1.ComponentEndpoint{
									Port: 8081,
									Host: "127.0.0.1",
								},
							},
							APIServer: controlplanev1alpha1.APIServerSpec{
								Sans: []string{"api.example.com"},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}

			By("creating the WorkerJoinToken pointing at that Runtime")
			workerjointoken := &controlplanev1alpha1.WorkerJoinToken{}
			if err := k8sClient.Get(ctx, typeNamespacedName, workerjointoken); err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.WorkerJoinToken{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: controlplanev1alpha1.WorkerJoinTokenSpec{
						RuntimeRef: corev1.LocalObjectReference{Name: runtimeName},
						TTL:        metav1.Duration{Duration: time.Hour},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			By("Cleanup the WorkerJoinToken")
			resource := &controlplanev1alpha1.WorkerJoinToken{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				controllerReconciler := &WorkerJoinTokenReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}
				_, _ = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})

				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &controlplanev1alpha1.WorkerJoinToken{}))
				}).Should(BeTrue())
			} else {
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}

			By("Cleanup the Runtime")
			runtimeResource := &controlplanev1alpha1.Runtime{}
			if err := k8sClient.Get(ctx, runtimeNamespacedName, runtimeResource); err == nil {
				Expect(k8sClient.Delete(ctx, runtimeResource)).To(Succeed())
			}
		})

		It("reports Degraded because the runtime has no admin kubeconfig secret yet", func() {
			controllerReconciler := &WorkerJoinTokenReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("first reconcile: registers the finalizer without minting anything")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("second reconcile: fails because the runtime has no admin kubeconfig secret yet")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("RuntimeNotReady"))
			Expect(err.Error()).To(ContainSubstring("admin kubeconfig secret"))

			resource := &controlplanev1alpha1.WorkerJoinToken{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			cond := meta.FindStatusCondition(resource.Status.Conditions, typeReadyWorkerJoinToken)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("RuntimeNotReady"))
			Expect(resource.Status.TokenID).To(BeEmpty(), "no token should have been minted")
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
			_ = k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "default"}})
		})

		It("reports drift when status.secretRef has not been set yet", func() {
			joinToken := &controlplanev1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test", Namespace: "default"},
			}

			drifted, err := reconciler.secretDrifted(ctx, joinToken)
			Expect(err).NotTo(HaveOccurred())
			Expect(drifted).To(BeTrue())
		})

		It("reports drift when the referenced secret no longer exists", func() {
			joinToken := &controlplanev1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test", Namespace: "default"},
				Status: controlplanev1alpha1.WorkerJoinTokenStatus{
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
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "default"},
				Data:       map[string][]byte{"jointoken": []byte("tampered-content")},
			})).To(Succeed())

			joinToken := &controlplanev1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test", Namespace: "default"},
				Status: controlplanev1alpha1.WorkerJoinTokenStatus{
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
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "default"},
				Data:       map[string][]byte{"jointoken": content},
			})).To(Succeed())

			joinToken := &controlplanev1alpha1.WorkerJoinToken{
				ObjectMeta: metav1.ObjectMeta{Name: "drift-test", Namespace: "default"},
				Status: controlplanev1alpha1.WorkerJoinTokenStatus{
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
