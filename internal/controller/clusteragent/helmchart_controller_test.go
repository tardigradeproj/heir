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
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	heirruntime "github.com/tardigradeproj/heir/pkg/runtime"
)

var _ = Describe("HelmChart Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		helmchart := &clusteragentv1alpha1.HelmChart{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind HelmChart")
			err := k8sClient.Get(ctx, typeNamespacedName, helmchart)
			if err != nil && errors.IsNotFound(err) {
				resource := &clusteragentv1alpha1.HelmChart{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: clusteragentv1alpha1.HelmChartSpec{
						Chart: clusteragentv1alpha1.ChartSpec{
							Name: "podinfo",
							Repo: "https://stefanprodan.github.io/podinfo",
						},
						// runtime.image is deliberately left unset here: it must come from the
						// CRD's default (see +kubebuilder:default={} on HelmChartSpec.Runtime).
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &clusteragentv1alpha1.HelmChart{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance HelmChart")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("reconciling until the finalizer removes it, so it doesn't leak into the next spec")
			controllerReconciler := &HelmChartReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			Eventually(func() error {
				if _, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName}); err != nil {
					return err
				}
				return k8sClient.Get(ctx, typeNamespacedName, &clusteragentv1alpha1.HelmChart{})
			}).Should(Satisfy(errors.IsNotFound))
		})

		It("creates a ServiceAccount, ClusterRoleBinding, and install Job for the chart", func() {
			controllerReconciler := &HelmChartReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("reconciling once to add the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("reconciling again to create the chart's resources")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("checking the service account was created in the HelmChart's namespace")
			sa := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      heirruntime.ServiceAccountName(resourceName),
				Namespace: "default",
			}, sa)).To(Succeed())

			By("checking the clusterrolebinding was created")
			crb := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: heirruntime.ServiceAccountName(resourceName),
			}, crb)).To(Succeed())

			By("checking the install job was created in the HelmChart's namespace")
			job := &batchv1.Job{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      heirruntime.JobName(resourceName, heirruntime.JobOperationInstall),
				Namespace: "default",
			}, job)).To(Succeed())
		})

		It("recreates the install job once the chart's spec changes, after it finishes running", func() {
			controllerReconciler := &HelmChartReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			jobKey := types.NamespacedName{
				Name:      heirruntime.JobName(resourceName, heirruntime.JobOperationInstall),
				Namespace: "default",
			}

			By("reconciling to add the finalizer, then to create the install job")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			originalJob := &batchv1.Job{}
			Expect(k8sClient.Get(ctx, jobKey, originalJob)).To(Succeed())
			originalUID := originalJob.UID

			By("marking the install job complete, since envtest runs no real Job controller")
			markJobComplete(ctx, originalJob)

			By("changing the chart's version, which bumps the HelmChart's generation")
			resource := &clusteragentv1alpha1.HelmChart{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			resource.Spec.Chart.Version = "v2.0.0"
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			By("reconciling deletes the now-stale, completed install job")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				return k8sClient.Get(ctx, jobKey, &batchv1.Job{})
			}).Should(Satisfy(errors.IsNotFound))

			By("reconciling again creates a fresh install job for the new generation")
			Eventually(func() error {
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				if err != nil {
					return err
				}
				return k8sClient.Get(ctx, jobKey, &batchv1.Job{})
			}).Should(Succeed())

			newJob := &batchv1.Job{}
			Expect(k8sClient.Get(ctx, jobKey, newJob)).To(Succeed())
			Expect(newJob.UID).NotTo(Equal(originalUID))

			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(newJob.Labels[heirruntime.ObservedGenerationLabel]).To(Equal(strconv.FormatInt(resource.Generation, 10)))
		})

		It("does not delete the install job while it is still running, even if the spec changed", func() {
			controllerReconciler := &HelmChartReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			jobKey := types.NamespacedName{
				Name:      heirruntime.JobName(resourceName, heirruntime.JobOperationInstall),
				Namespace: "default",
			}

			By("reconciling to add the finalizer, then to create the install job")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			runningJob := &batchv1.Job{}
			Expect(k8sClient.Get(ctx, jobKey, runningJob)).To(Succeed())
			runningUID := runningJob.UID

			By("changing the chart's version while the install job has no terminal condition")
			resource := &clusteragentv1alpha1.HelmChart{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			resource.Spec.Chart.Version = "v2.0.0"
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			By("reconciling leaves the still-running job alone")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			stillThere := &batchv1.Job{}
			Expect(k8sClient.Get(ctx, jobKey, stillThere)).To(Succeed())
			Expect(stillThere.UID).To(Equal(runningUID))
		})
	})
})

// markJobComplete sets job's status to Complete, standing in for the real Job controller that
// envtest doesn't run.
func markJobComplete(ctx context.Context, job *batchv1.Job) {
	now := metav1.Now()
	job.Status.StartTime = &now
	job.Status.CompletionTime = &now
	job.Status.Conditions = append(job.Status.Conditions,
		batchv1.JobCondition{
			Type:   batchv1.JobSuccessCriteriaMet,
			Status: corev1.ConditionTrue,
		},
		batchv1.JobCondition{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		},
	)
	Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())
}
