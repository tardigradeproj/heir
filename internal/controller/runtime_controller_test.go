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
	"crypto/x509"
	"encoding/pem"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
)

var _ = Describe("Runtime Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		runtime := &controlplanev1alpha1.Runtime{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Runtime")
			err := k8sClient.Get(ctx, typeNamespacedName, runtime)
			if err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &controlplanev1alpha1.Runtime{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Runtime")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &RuntimeReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

var _ = Describe("Runtime Controller Config", func() {
	Context("configuration configmap", func() {
		const resourceName = "test-config"

		ctx := context.Background()

		namespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the Runtime resource for the config test")
			err := k8sClient.Get(ctx, namespacedName, &controlplanev1alpha1.Runtime{})
			if err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: controlplanev1alpha1.RuntimeSpec{
						ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
							Heir: controlplanev1alpha1.HeirSpec{
								Image: "v1.32.0",
							},
							Service: controlplanev1alpha1.ServiceSpec{
								ServiceType: corev1.ServiceTypeClusterIP,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &controlplanev1alpha1.Runtime{}
			err := k8sClient.Get(ctx, namespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			By("cleaning up the Runtime resource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should create the configuration ConfigMap with all service script entries", func() {
			By("reconciling the Runtime resource")
			reconciler := &RuntimeReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("validating the config ConfigMap exists and contains all expected keys")
			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-config", resourceName),
				Namespace: "default",
			}, configMap)).To(Succeed())

			Expect(configMap.Data).To(HaveKey(layout.Config.APIServer.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.Config.ControllerManager.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.Config.Scheduler.SecretKey))
		})
	})
})

var _ = Describe("Runtime Controller PKI", func() {
	Context("pki config map", func() {
		const resourceName = "test-pki"

		ctx := context.Background()

		namespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the Runtime resource for the PKI test")
			err := k8sClient.Get(ctx, namespacedName, &controlplanev1alpha1.Runtime{})
			if err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: controlplanev1alpha1.RuntimeSpec{
						ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
							Heir: controlplanev1alpha1.HeirSpec{
								Image: "v1.32.0",
							},
							Service: controlplanev1alpha1.ServiceSpec{
								ServiceType: corev1.ServiceTypeClusterIP,
							},
						},
						UpstreamCluster: controlplanev1alpha1.UpstreamCluster{
							APIServer: controlplanev1alpha1.APIServerSpec{
								Sans: []string{
									"tardigrade.vm.co.mz",
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &controlplanev1alpha1.Runtime{}
			err := k8sClient.Get(ctx, namespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			By("cleaning up the Runtime resource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should create the PKI secret with all certificate and key entries", func() {
			By("reconciling the Runtime resource")
			reconciler := &RuntimeReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("validating the PKI auth secret exists and contains all expected keys")
			pkiSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pki-auth", resourceName),
				Namespace: "default",
			}, pkiSecret)).To(Succeed())

			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.CACert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.CAKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.APIServerCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.APIServerKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ServiceAccountCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ServiceAccountKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.AdminConf.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.ControllerManagerConf.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.SchedulerConf.SecretKey))

			By("decoding the apiserver certificate and validating its SANs")
			certPEM := pkiSecret.Data[layout.PKI.APIServerCert.SecretKey]
			block, _ := pem.Decode(certPEM)
			Expect(block).NotTo(BeNil(), "apiserver certificate PEM block should be decodable")

			cert, err := x509.ParseCertificate(block.Bytes)
			Expect(err).NotTo(HaveOccurred())

			// Collect all SANs: DNS names and IP addresses.
			var sans []string
			for _, dns := range cert.DNSNames {
				sans = append(sans, dns)
			}
			for _, ip := range cert.IPAddresses {
				sans = append(sans, ip.String())
			}

			// These are the default SANs injected by setupKubeApiServerAltNames when
			// no ExternalAddress or extra Sans are provided in the spec.
			Expect(sans).To(ContainElements(
				"127.0.0.1",
				"tardigrader.com",
				"tardigrade.vm.co.mz",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
				"kubernetes.default.cluster",
				"server.kubernetes.local",
				"api-server.kubernetes.local",
			))
			Expect(sans).To(HaveLen(9))
		})
	})
})

var _ = Describe("Runtime Controller Service", func() {
	Context("service reconciliation", func() {
		ctx := context.Background()

		newRuntime := func(name string, svcSpec controlplanev1alpha1.ServiceSpec) *controlplanev1alpha1.Runtime {
			return &controlplanev1alpha1.Runtime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: controlplanev1alpha1.RuntimeSpec{
					ControlPlane: controlplanev1alpha1.ControlPlaneSpec{
						Heir: controlplanev1alpha1.HeirSpec{Image: "v1.32.0"},
						Service:    svcSpec,
					},
				},
			}
		}

		reconcileAndGetService := func(name string) *corev1.Service {
			reconciler := &RuntimeReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, svc)).To(Succeed())
			return svc
		}

		It("should create a ClusterIP service exposing port 6443", func() {
			const name = "test-svc-basic"
			resource := newRuntime(name, controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			})
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, resource)).To(Succeed()) })

			svc := reconcileAndGetService(name)

			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(svc.Spec.Ports).To(ContainElement(
				HaveField("Port", int32(6443)),
			))
		})

		It("should include additional ports defined in the spec", func() {
			const name = "test-svc-extra-ports"
			proto := corev1.ProtocolTCP
			appProto := "https"
			resource := newRuntime(name, controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
				AdditionalPorts: []controlplanev1alpha1.AdditionalPort{
					{
						Name:        "metrics",
						Port:        9090,
						TargetPort:  intstr.FromInt32(9090),
						Protocol:    proto,
						AppProtocol: &appProto,
					},
				},
			})
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, resource)).To(Succeed()) })

			svc := reconcileAndGetService(name)

			Expect(svc.Spec.Ports).To(HaveLen(2))
			Expect(svc.Spec.Ports).To(ContainElement(HaveField("Port", int32(6443))))
			Expect(svc.Spec.Ports).To(ContainElement(HaveField("Port", int32(9090))))
		})

		It("should set the correct selector labels", func() {
			const name = "test-svc-selector"
			resource := newRuntime(name, controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			})
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, resource)).To(Succeed()) })

			svc := reconcileAndGetService(name)

			Expect(svc.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/name", name))
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "heir"))
		})

		It("should update ports when the spec changes", func() {
			const name = "test-svc-update"
			resource := newRuntime(name, controlplanev1alpha1.ServiceSpec{
				ServiceType: corev1.ServiceTypeClusterIP,
			})
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, resource)).To(Succeed()) })

			// First reconcile — only apiserver port.
			svc := reconcileAndGetService(name)
			Expect(svc.Spec.Ports).To(HaveLen(1))

			// Add an extra port to the spec and reconcile again.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, resource)).To(Succeed())
			resource.Spec.ControlPlane.Service.AdditionalPorts = []controlplanev1alpha1.AdditionalPort{
				{Name: "extra", Port: 8080, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP},
			}
			Expect(k8sClient.Update(ctx, resource)).To(Succeed())

			svc = reconcileAndGetService(name)
			Expect(svc.Spec.Ports).To(HaveLen(2))
			Expect(svc.Spec.Ports).To(ContainElement(HaveField("Port", int32(8080))))
		})
	})
})
