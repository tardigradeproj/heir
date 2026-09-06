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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tardigradeproj/heir/test/utils"
)

// provisionRuntimeSpec is the body of the "should provision runtime" spec, registered
// against the shared "Manager" Describe/Context tree in e2e_test.go. It provisions a
// Runtime, mints a WorkerJoinToken against it, and joins a real worker node using the
// resulting token. Cleanup of everything it creates lives in that file's AfterEach —
// see the comment there for why it can't live in a DeferCleanup here.
func provisionRuntimeSpec() {
	By("creating a Runtime custom resource")
	def := utils.RuntimeManifestDefault()
	def.Name = runtimeName
	def.Namespace = runtimeNamespace
	def.ControlPlaneImage = heirImage
	def.ClusterAgentImage = heirClusterAgent
	def.PlaneTunnelServerImage = heirTunnelImage
	def.ControlPlaneExternalEndpoint = kindNodeIP
	runtimeManifest, err := utils.BasicRuntimeManifest(*def)
	Expect(err).NotTo(HaveOccurred(), "Failed to render Runtime manifest")

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(runtimeManifest)
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to create Runtime resource")
	// Cleanup lives in AfterEach, not DeferCleanup here — see the comment there.

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

	By("retrieving the tenant admin kubeconfig")
	cmd = exec.Command("kubectl", "get", "secret", runtimeName+"-kubeconfig",
		"-n", runtimeNamespace, "-o", "jsonpath={.data.kubeconfig}",
	)
	encodedKubeconfig, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "tenant kubeconfig secret should exist")
	Expect(encodedKubeconfig).NotTo(BeEmpty())

	decodedKubeconfig, err := base64.StdEncoding.DecodeString(encodedKubeconfig)
	Expect(err).NotTo(HaveOccurred(), "tenant kubeconfig value should be valid base64")
	tenantConfig, err := clientcmd.Load(decodedKubeconfig)
	Expect(err).NotTo(HaveOccurred(), "tenant kubeconfig value should parse")

	// GenerateClientKubeconfigAuthSecret (pkg/runtime/pki_auth.go) points the server at
	// endpoint.APIServer.Host — the Runtime's external endpoint, i.e. the kind
	// docker-network IP — which is only reachable from inside that network (e.g. the
	// worker container), not from this test process. The API server's NodePort is
	// published to the host at 127.0.0.1, so rewrite the server address to that before
	// using this kubeconfig here.
	apiServerURL := fmt.Sprintf("https://127.0.0.1:%d", def.ApiServerNodePort)
	for _, cluster := range tenantConfig.Clusters {
		cluster.Server = apiServerURL
	}

	rewrittenKubeconfig, err := clientcmd.Write(*tenantConfig)
	Expect(err).NotTo(HaveOccurred())

	kubeconfigPath := tenantKubeconfigPath()
	Expect(os.WriteFile(kubeconfigPath, rewrittenKubeconfig, 0o600)).To(Succeed())

	By("creating a WorkerJoinToken for the provisioned runtime")
	workerJoinTokenName := runtimeName + "-join"
	workerJoinTokenManifest := fmt.Sprintf(`
apiVersion: clusteragent.tardigrade.runtime.io/v1alpha1
kind: WorkerJoinToken
metadata:
  name: %s
spec:
  ttl: 3h
`, workerJoinTokenName)
	cmd = exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(workerJoinTokenManifest)
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to create WorkerJoinToken resource")
	// Cleanup lives in AfterEach, not DeferCleanup here — see the comment there.

	By("waiting for the WorkerJoinToken to report a Ready status condition")
	verifyWorkerJoinTokenReady := func(g Gomega) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "workerjointoken", workerJoinTokenName,
			"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(output).To(Equal("True"), "WorkerJoinToken not yet Ready")
	}
	Eventually(verifyWorkerJoinTokenReady, 3*time.Minute, 5*time.Second).Should(Succeed())

	By("fetching the minted token ID from the WorkerJoinToken status")
	cmd = exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "workerjointoken", workerJoinTokenName,
		"-o", "jsonpath={.status.tokenID}",
	)
	tokenID, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	Expect(tokenID).NotTo(BeEmpty(), "WorkerJoinToken status should report a tokenID")

	By("validating the contents of the join token secret")
	cmd = exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "secret", workerJoinTokenName+"-jointoken",
		"-n", "kube-system", "-o", "jsonpath={.data.jointoken}",
	)
	encoded, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "join token secret should exist")
	Expect(encoded).NotTo(BeEmpty(), "join token secret should have a jointoken key")

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	Expect(err).NotTo(HaveOccurred(), "jointoken value should be valid base64")
	Expect(decoded).NotTo(BeEmpty(), "jointoken value should decode to a non-empty kubeconfig")

	By("provisioning a worker node")
	workerDir := filepath.Join(os.TempDir(), workerClusterName)
	Expect(os.MkdirAll(workerDir, 0o700)).To(Succeed())

	const workerNodeName = "worker%d"
	_, _, err = utils.ProvisionWorkerNode(workerClusterName, workerNodeName, workerDir)
	Expect(err).NotTo(HaveOccurred(), "Failed to provision worker node")
	// Cleanup lives in AfterEach, not DeferCleanup here — see the comment there.
	// workerContainerName ("<workerClusterName>-worker0") is a package-level
	// const rather than something derived via Inspect(), so AfterEach (which
	// has no access to this spec's locals) can reference the same name.

	By("joining the worker node to the runtime using the minted join token")
	verifyWorkerJoined := func(g Gomega) {
		cmd := exec.Command("docker", "exec", workerContainerName,
			"heir", "provision", "worker", "--token", encoded,
		)
		_, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
	}
	Eventually(verifyWorkerJoined, 2*time.Minute, 5*time.Second).Should(Succeed())

	By("listing the nodes on the tenant cluster")
	verifyTenantNodeCount := func(g Gomega) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "nodes",
			"-o", "jsonpath={.items[*].metadata.name}",
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		nodeNames := strings.Fields(output)
		fmt.Fprintf(GinkgoWriter, "node name: %v\n", nodeNames)
		g.Expect(nodeNames).To(HaveLen(1), "expected exactly 1 node on the tenant cluster, got %v", nodeNames)
	}
	Eventually(verifyTenantNodeCount, 3*time.Minute, 5*time.Second).Should(Succeed())

	By("validating that the tenant node is Ready")
	verifyTenantNodeReady := func(g Gomega) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "nodes",
			"-o", `jsonpath={.items[0].status.conditions[?(@.type=="Ready")].status}`,
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		fmt.Fprintf(GinkgoWriter, "node readiness: %v\n", output)
		g.Expect(output).To(Equal("True"), "tenant node not yet Ready")
	}
	Eventually(verifyTenantNodeReady, 5*time.Minute, 5*time.Second).Should(Succeed())

	By("reading logs from the flannel CNI pod on the tenant cluster")
	var flannelPodName string
	verifyFlannelPodRunning := func(g Gomega) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "pods",
			"-n", "kube-flannel", "-l", "app=flannel",
			"-o", "jsonpath={.items[0].metadata.name}",
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(output).NotTo(BeEmpty(), "expected a flannel pod to exist in the kube-flannel namespace")
		flannelPodName = output

		cmd = exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "get", "pod", flannelPodName,
			"-n", "kube-flannel",
			"-o", `jsonpath={.status.containerStatuses[?(@.name=="kube-flannel")].ready}`,
		)
		output, err = utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(output).To(Equal("true"), "flannel container not yet ready")
	}
	Eventually(verifyFlannelPodRunning, 3*time.Minute, 5*time.Second).Should(Succeed())

	var flannelLogs string
	verifyFlannelPodLogsReadable := func(g Gomega) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "logs", flannelPodName,
			"-n", "kube-flannel", "-c", "kube-flannel",
		)
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "failed to read logs from flannel pod %s", flannelPodName)
		flannelLogs = output
	}
	Eventually(verifyFlannelPodLogsReadable, 3*time.Minute, 5*time.Second).Should(Succeed())
	fmt.Fprintf(GinkgoWriter, "flannel logs:\n%s\n", flannelLogs)
	Expect(flannelLogs).NotTo(BeEmpty(), "expected non-empty logs from flannel pod")
	Expect(strings.ToLower(flannelLogs)).NotTo(ContainSubstring("panic"), "flannel logs should not contain a panic")
}
