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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	heirruntime "github.com/tardigradeproj/heir/pkg/runtime"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
)

// certInfo holds the decoded fields of an x509 certificate relevant for testing.
type certInfo struct {
	CN        string
	SANs      []string
	NotBefore time.Time
	NotAfter  time.Time
}

// parseCertPEM decodes a PEM-encoded certificate and returns its key fields.
// DNS names and IP addresses are both collected into SANs.
func parseCertPEM(pemBytes []byte) certInfo {
	GinkgoHelper()
	block, _ := pem.Decode(pemBytes)
	Expect(block).NotTo(BeNil(), "certificate PEM block must be decodable")
	cert, err := x509.ParseCertificate(block.Bytes)
	Expect(err).NotTo(HaveOccurred(), "certificate must be parseable")

	sans := append([]string{}, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	return certInfo{
		CN:        cert.Subject.CommonName,
		SANs:      sans,
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
	}
}

var _ = Describe("Runtime Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()
		namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

		BeforeEach(func() {
			By("creating the Runtime resource")
			err := k8sClient.Get(ctx, namespacedName, &controlplanev1alpha1.Runtime{})
			if err != nil && errors.IsNotFound(err) {
				resource := &controlplanev1alpha1.Runtime{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
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
		})

		AfterEach(func() {
			resource := &controlplanev1alpha1.Runtime{}
			err := k8sClient.Get(ctx, namespacedName, resource)
			Expect(err).NotTo(HaveOccurred())
			By("cleaning up the Runtime resource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should create all expected control-plane resources", func() {
			By("reconciling the Runtime resource")
			reconciler := &RuntimeReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(32),
				WrkCtx:   typ.NewWorkerContextWithDefaults(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the PKI Secret is created with CA and component certificates")
			pkiSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, namespacedName, pkiSecret)).To(Succeed())
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.CACert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.CAKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.APIServerCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.APIServerKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ServiceAccountCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ServiceAccountKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.PlaneTunnelKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.PlaneTunnelCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ApiServerPlaneTunnelKey.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.PKI.ApiServerPlaneTunnelCert.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.AdminConf.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.ControllerManagerConf.SecretKey))
			Expect(pkiSecret.Data).To(HaveKey(layout.Auth.SchedulerConf.SecretKey))

			By("verifying the API server certificate CN, SANs, and expiration")
			apiServerCert := parseCertPEM(pkiSecret.Data[layout.PKI.APIServerCert.SecretKey])
			Expect(apiServerCert.CN).To(Equal("kube-apiserver"))
			Expect(apiServerCert.SANs).To(ContainElements(
				"api.example.com",
				"127.0.0.1",
				fmt.Sprintf("%s.default.svc.cluster.local", resourceName),
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
			))
			Expect(apiServerCert.NotAfter).To(BeTemporally(">", time.Now()))
			Expect(apiServerCert.NotAfter).To(
				BeTemporally("~", apiServerCert.NotBefore.Add(heirruntime.CertificateDuration), time.Minute),
			)

			By("verifying the API Server plane tunnel egress selector certificate SANs and expiration")
			APIServerPlaneTunnelEgressSelectorCert := parseCertPEM(pkiSecret.Data[layout.PKI.ApiServerPlaneTunnelCert.SecretKey])
			Expect(APIServerPlaneTunnelEgressSelectorCert.SANs).To(ContainElements(fmt.Sprintf("%s-tunnel-egress", resourceName)))

			By("verifying the plane tunnel certificate SANs and expiration")
			planeTunnelCert := parseCertPEM(pkiSecret.Data[layout.PKI.PlaneTunnelCert.SecretKey])
			Expect(planeTunnelCert.SANs).To(ContainElement("127.0.0.1"))
			Expect(planeTunnelCert.NotAfter).To(BeTemporally(">", time.Now()))
			Expect(planeTunnelCert.NotAfter).To(
				BeTemporally("~", planeTunnelCert.NotBefore.Add(heirruntime.CertificateDuration), time.Minute),
			)

			By("verifying the configuration ConfigMap is created with all service scripts")
			configMap := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, namespacedName, configMap)).To(Succeed())
			Expect(configMap.Data).To(HaveKey(layout.Config.APIServer.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.Config.ControllerManager.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.Config.Scheduler.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.Config.EgressSelector.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.StaticManifest.Bootstrap.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.StaticManifest.NodeProfile.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.StaticManifest.Coredns.SecretKey))
			Expect(configMap.Data).To(HaveKey(layout.StaticManifest.KubeProxy.SecretKey))

			By("verifying the control-plane Service is created")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, namespacedName, svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(svc.Spec.Ports).To(ContainElement(HaveField("Port", int32(6443))))

			By("verifying the control-plane Deployment is created")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, namespacedName, deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			By("verifying the plane tunnel Deployment is created")
			planeTunnelDeployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-tunnel", resourceName), Namespace: "default"}, planeTunnelDeployment)).To(Succeed())
			Expect(planeTunnelDeployment.Spec.Template.Spec.Containers).To(HaveLen(1))

			By("verifying the plane tunnel headless Service is created")
			planeTunnelHeadlessSvc := &corev1.Service{}
			Expect(k8sClient.
				Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-tunnel-headless", resourceName), Namespace: "default"}, planeTunnelHeadlessSvc)).
				To(Succeed())
			Expect(planeTunnelHeadlessSvc.Spec.ClusterIP).To(Equal("None"))
			Expect(planeTunnelHeadlessSvc.Spec.Ports).To(HaveLen(1))
			Expect(planeTunnelHeadlessSvc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(8443)))

			By("verifying the plane tunnel worker agent Service is created")
			planeTunnelWorkerAgentSvc := &corev1.Service{}
			Expect(k8sClient.
				Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-tunnel", resourceName), Namespace: "default"}, planeTunnelWorkerAgentSvc)).
				To(Succeed())
			Expect(planeTunnelWorkerAgentSvc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			Expect(planeTunnelWorkerAgentSvc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(9445)))
			Expect(planeTunnelWorkerAgentSvc.Spec.Ports[0].NodePort).To(Equal(int32(30081)))

			By("verifying the plane tunnel egress selector service is created")
			planeTunnelEgressSelectorSvc := &corev1.Service{}
			Expect(k8sClient.
				Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-tunnel-egress", resourceName), Namespace: "default"}, planeTunnelEgressSelectorSvc)).
				To(Succeed())
			Expect(planeTunnelEgressSelectorSvc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(planeTunnelEgressSelectorSvc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(9443)))
			Expect(planeTunnelEgressSelectorSvc.Spec.Ports[0].Name).To(Equal("egress-selector"))
		})
	})
})
