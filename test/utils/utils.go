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

package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
)

const (
	certmanagerVersion = "v1.19.2"
	certmanagerURLTmpl = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"

	defaultKindBinary  = "kind"
	defaultKindCluster = "kind"
)

func warnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "warning: %v\n", err)
}

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// UninstallCertManager uninstalls the cert manager
func UninstallCertManager() {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}

	// Delete leftover leases in kube-system (not cleaned by default)
	kubeSystemLeases := []string{
		"cert-manager-cainjector-leader-election",
		"cert-manager-controller",
	}
	for _, lease := range kubeSystemLeases {
		cmd = exec.Command("kubectl", "delete", "lease", lease,
			"-n", "kube-system", "--ignore-not-found", "--force", "--grace-period=0")
		if _, err := Run(cmd); err != nil {
			warnError(err)
		}
	}
}

// InstallCertManager installs the cert manager bundle.
func InstallCertManager() error {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion)
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Wait for cert-manager-webhook to be ready, which can take time if cert-manager
	// was re-installed after uninstalling on a cluster.
	cmd = exec.Command("kubectl", "wait", "deployment.apps/cert-manager-webhook",
		"--for", "condition=Available",
		"--namespace", "cert-manager",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsCertManagerCRDsInstalled checks if any Cert Manager CRDs are installed
// by verifying the existence of key CRDs related to Cert Manager.
func IsCertManagerCRDsInstalled() bool {
	// List of common Cert Manager CRDs
	certManagerCRDs := []string{
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io",
		"orders.acme.cert-manager.io",
		"challenges.acme.cert-manager.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Cert Manager CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range certManagerCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// kindClusterName returns the KIND_CLUSTER environment override, or defaultKindCluster
// when unset.
func kindClusterName() string {
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		return v
	}
	return defaultKindCluster
}

// LoadImageToKindClusterWithName loads a local docker image to the kind cluster
func LoadImageToKindClusterWithName(name string) error {
	kindOptions := []string{"load", "docker-image", name, "--name", kindClusterName()}
	kindBinary := defaultKindBinary
	if v, ok := os.LookupEnv("KIND"); ok {
		kindBinary = v
	}
	cmd := exec.Command(kindBinary, kindOptions...)
	_, err := Run(cmd)
	return err
}

// KindControlPlaneIPv4 returns the IPv4 address of the Kind cluster's control-plane
// node container, as seen on the docker "kind" network. Only valid for single-node
// clusters, where that node's container is named "<cluster>-control-plane".
func KindControlPlaneIPv4() (string, error) {
	containerName := kindClusterName() + "-control-plane"
	cmd := exec.Command("docker", "inspect",
		"-f", `{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}`,
		containerName,
	)
	output, err := Run(cmd)
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(output)
	if ip == "" {
		return "", fmt.Errorf("no IPv4 address found for kind container %q", containerName)
	}
	return ip, nil
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.SplitSeq(output, "\n")
	for element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

// UncommentCode searches for target in the file and remove the comment prefix
// of the target content. The target content may span multiple lines.
func UncommentCode(filename, target, prefix string) error {
	// false positive
	// nolint:gosec
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", filename, err)
	}
	strContent := string(content)

	idx := strings.Index(strContent, target)
	if idx < 0 {
		return fmt.Errorf("unable to find the code %q to be uncomment", target)
	}

	out := new(bytes.Buffer)
	_, err = out.Write(content[:idx])
	if err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(target))
	if !scanner.Scan() {
		return nil
	}
	for {
		if _, err = out.WriteString(strings.TrimPrefix(scanner.Text(), prefix)); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
		// Avoid writing a newline in case the previous line was the last in target.
		if !scanner.Scan() {
			break
		}
		if _, err = out.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
	}

	if _, err = out.Write(content[idx+len(target):]); err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	// false positive
	// nolint:gosec
	if err = os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", filename, err)
	}

	return nil
}

// linuxHeirBinaryPath returns the absolute path to the goreleaser-built "heir" distro
// binary for the host architecture. It is anchored on GetProjectDir rather than a
// CWD-relative path, since Run changes the process's working directory as a side
// effect, making plain relative paths resolve differently depending on call order.
func linuxHeirBinaryPath() (string, error) {
	projectDir, err := GetProjectDir()
	if err != nil {
		return "", err
	}
	rel := "dist/distro_linux_amd64_v1/heir"
	if runtime.GOARCH == "arm64" {
		rel = "dist/distro_linux_arm64_v8.0/heir"
	}
	return filepath.Join(projectDir, rel), nil
}
func ProvisionWorkerNode(name string, nodeName string, workerDir string) (*config.Config, *cluster.Cluster, error) {
	workerBinaryPath, err := linuxHeirBinaryPath()
	if err != nil {
		return nil, nil, err
	}
	workerCfg := config.Config{
		Cluster: config.Cluster{
			Name:       name,
			PrivateKey: filepath.Join(workerDir, "id_rsa"),
		},
		Machines: []config.MachineReplicas{
			{
				Count: 1,
				Spec: &config.Machine{
					Image:      "quay.io/k0sproject/bootloose-ubuntu24.04",
					Name:       nodeName,
					Privileged: true,
					PortMappings: []config.PortMapping{
						{ContainerPort: 22},
					},
					Networks: []string{"kind"},
					Volumes: []config.Volume{
						{
							Type:        "bind",
							Source:      workerBinaryPath,
							Destination: "/usr/local/bin/heir",
						},
						{
							// Named volume so containerd's root (/var/lib/heir/containerd)
							// sits on ext4, not Docker's overlayfs — prevents the
							// "failed to mount rootfs: invalid argument" error.
							Type:        "volume",
							Destination: "/var/lib/heir",
						},
						{
							Type:        "bind",
							Source:      "/usr/lib/modules",
							Destination: "/usr/lib/modules",
							ReadOnly:    true,
						},
						{
							Type:        "bind",
							Source:      "/lib/modules",
							Destination: "/lib/modules",
							ReadOnly:    true,
						},
					},
				},
			},
		},
	}
	workerCluster, err := cluster.New(workerCfg)
	if err != nil {
		return nil, nil, err
	}
	err = workerCluster.Create()
	if err != nil {
		return nil, nil, err
	}
	return &workerCfg, workerCluster, nil
}

type RuntimeManifest struct {
	Name                         string
	Namespace                    string
	Replicas                     int
	ControlPlaneImage            string
	PlaneTunnelServerImage       string
	ApiServerNodePort            int
	PlaneTunnelNodePort          int
	ControlPlaneExternalEndpoint string
}

func RuntimeManifestDefault() *RuntimeManifest {
	return &RuntimeManifest{
		Namespace:                    "default",
		Replicas:                     1,
		ApiServerNodePort:            30080,
		PlaneTunnelNodePort:          30081,
		ControlPlaneExternalEndpoint: "kind",
	}
}

func BasicRuntimeManifest(s RuntimeManifest) (string, error) {
	tmplStr := `
apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  controlPlane:
    heir:
      image: {{.ControlPlaneImage}}
    deployment:
      replicas: {{.Replicas}}
      serviceAccountName: default
    service:
      serviceType: NodePort
      apiServerNodePort: {{.ApiServerNodePort}}
    planeTunnel:
      server:
        image: {{.PlaneTunnelServerImage}}
        deployment:
          replicas: 2
      service:
        serviceType: NodePort
        nodePort: {{.PlaneTunnelNodePort}}
  cluster:
    apiServer:
      sans: ["master0", "kind"]
      extraArgs:
        advertise-address: "10.0.2.2"
    controllerManager:
      extraArgs: {}
    scheduler:
      extraArgs: {}
    controlPlaneExternalEndpoint:
      apiServer:
        host: {{.ControlPlaneExternalEndpoint}}
        port: {{.ApiServerNodePort}}
      planeTunnel:
        host: {{.ControlPlaneExternalEndpoint}}
        port: {{.PlaneTunnelNodePort}}
    storage:
      type: kine
`
	tmpl := template.Must(template.New("todoTmpl").Parse(tmplStr))
	var tplOutput bytes.Buffer
	if err := tmpl.Execute(&tplOutput, s); err != nil {
		return "", err
	}
	return tplOutput.String(), nil
}
