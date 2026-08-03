//go:build e2e
// +build e2e

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

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/tardigradeproj/heir/test/utils"
)

// namespace where the project is deployed in
const namespace = "heir-system"

// serviceAccountName created for the project
const serviceAccountName = "heir-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "heir-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "heir-metrics-binding"

// runtimeNamespace is the namespace in which the provisioned Runtime custom resource lives.
// It must not be the restricted-PodSecurity manager namespace, since the control-plane pod
// generated for a Runtime does not set the securityContext fields "restricted" requires.
const runtimeNamespace = "default"

// runtimeName is the name of the Runtime custom resource created by the provisioning test.
const runtimeName = "e2e-runtime"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string
	// kindNodeIP is the docker-network IPv4 address of the (single) Kind control-plane
	// node, used as the externally reachable host for Runtimes provisioned in these tests.
	var kindNodeIP string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("reading the kind control-plane node IPv4 address")
		var err error
		kindNodeIP, err = utils.KindControlPlaneIPv4()
		Expect(err).NotTo(HaveOccurred(), "Failed to read kind control-plane node IPv4 address")
		Expect(kindNodeIP).NotTo(BeEmpty())

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", controllerManagerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=heir-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
		It("should provision runtime", func() {
			By("creating a Runtime custom resource")
			def := utils.RuntimeManifestDefault()
			def.Name = runtimeName
			def.Namespace = runtimeNamespace
			def.ControlPlaneImage = heirImage
			def.PlaneTunnelServerImage = heirTunnelImage
			def.ControlPlaneExternalEndpoint = kindNodeIP
			runtimeManifest, err := utils.BasicRuntimeManifest(*def)
			Expect(err).NotTo(HaveOccurred(), "Failed to render Runtime manifest")

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(runtimeManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Runtime resource")

			DeferCleanup(func() {
				By("deleting the Runtime custom resource")
				cmd := exec.Command("kubectl", "delete", "runtime", runtimeName,
					"-n", runtimeNamespace, "--ignore-not-found", "--wait=false")
				_, _ = utils.Run(cmd)
			})

			By("waiting for the Runtime to report an Available status condition")
			verifyRuntimeAvailable := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "runtime", runtimeName,
					"-n", runtimeNamespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Available")].status}`,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Runtime not yet Available")
			}
			Eventually(verifyRuntimeAvailable, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("validating that the control-plane deployment has a ready replica")
			verifyControlPlaneDeploymentReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", runtimeName,
					"-n", runtimeNamespace, "-o", "jsonpath={.status.readyReplicas}",
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("1"), "control-plane deployment not yet ready")
			}
			Eventually(verifyControlPlaneDeploymentReady, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("validating that the control-plane service was created")
			cmd = exec.Command("kubectl", "get", "service", runtimeName, "-n", runtimeNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "control-plane service should exist")

			By("creating a WorkerJoinToken for the provisioned runtime")
			workerJoinTokenName := runtimeName + "-join"
			workerJoinTokenManifest := fmt.Sprintf(`
apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: WorkerJoinToken
metadata:
  name: %s
  namespace: %s
spec:
  runtimeRef:
    name: %s
`, workerJoinTokenName, runtimeNamespace, runtimeName)
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(workerJoinTokenManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create WorkerJoinToken resource")

			DeferCleanup(func() {
				By("deleting the WorkerJoinToken custom resource")
				cmd := exec.Command("kubectl", "delete", "workerjointoken", workerJoinTokenName,
					"-n", runtimeNamespace, "--ignore-not-found", "--wait=false")
				_, _ = utils.Run(cmd)
			})

			By("waiting for the WorkerJoinToken to report a Ready status condition")
			verifyWorkerJoinTokenReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "workerjointoken", workerJoinTokenName,
					"-n", runtimeNamespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "WorkerJoinToken not yet Ready")
			}
			Eventually(verifyWorkerJoinTokenReady, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("fetching the minted token ID from the WorkerJoinToken status")
			cmd = exec.Command("kubectl", "get", "workerjointoken", workerJoinTokenName,
				"-n", runtimeNamespace, "-o", "jsonpath={.status.tokenID}",
			)
			tokenID, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(tokenID).NotTo(BeEmpty(), "WorkerJoinToken status should report a tokenID")

			By("validating the contents of the join token secret")
			cmd = exec.Command("kubectl", "get", "secret", workerJoinTokenName+"-jointoken",
				"-n", runtimeNamespace, "-o", "jsonpath={.data.jointoken}",
			)
			encoded, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "join token secret should exist")
			Expect(encoded).NotTo(BeEmpty(), "join token secret should have a jointoken key")

			decoded, err := base64.StdEncoding.DecodeString(encoded)
			Expect(err).NotTo(HaveOccurred(), "jointoken value should be valid base64")
			Expect(decoded).NotTo(BeEmpty(), "jointoken value should decode to a non-empty kubeconfig")

			By("provisioning a worker node")
			workerClusterName := runtimeName + "-worker"
			workerDir := filepath.Join(os.TempDir(), workerClusterName)
			Expect(os.MkdirAll(workerDir, 0o700)).To(Succeed())

			const workerNodeName = "worker%d"
			_, workerCluster, err := utils.ProvisionWorkerNode(workerClusterName, workerNodeName, workerDir)
			Expect(err).NotTo(HaveOccurred(), "Failed to provision worker node")

			DeferCleanup(func() {
				By("deleting the worker node")
				_ = workerCluster.Delete()
				_ = os.RemoveAll(workerDir)
			})

			// ProvisionWorkerNode always creates exactly one machine, so it's always index 0.
			machines, err := workerCluster.Inspect(nil)
			Expect(err).NotTo(HaveOccurred(), "Failed to inspect worker node")
			Expect(machines).To(HaveLen(1))
			workerContainerName := machines[0].ContainerName()

			By("joining the worker node to the runtime using the minted join token")
			verifyWorkerJoined := func(g Gomega) {
				cmd := exec.Command("docker", "exec", workerContainerName,
					"heir", "provision", "worker", "--token", encoded,
				)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyWorkerJoined, 2*time.Minute, 5*time.Second).Should(Succeed())
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
