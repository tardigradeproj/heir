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
	"fmt"
	"os"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tardigradeproj/heir/test/utils"
)

var arch = runtime.GOARCH

var (
	// managerImage is the manager image to be built and loaded for testing.
	controllerManagerImage = fmt.Sprintf("ghcr.io/tardigradeproj/heir-controller-manager:latest-%s", arch)
	heirImage              = fmt.Sprintf("ghcr.io/tardigradeproj/heir:latest-%s", arch)
	heirTunnelImage        = fmt.Sprintf("ghcr.io/tardigradeproj/heir-tunnel:latest-%s", arch)

	// shouldCleanupCertManager tracks whether CertManager was installed by this suite.
	shouldCleanupCertManager = false
)

// TestE2E runs the e2e test suite to validate the solution in an isolated environment.
// The default setup requires Kind and CertManager.
//
// To skip CertManager installation, set: CERT_MANAGER_INSTALL_SKIP=true
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting heir e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	//By("building images")
	//cmd := exec.Command("make", "build-bins")
	//_, err := utils.Run(cmd)
	//ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager image")

	// ensure the image is built and available, then remove the following block.
	By(fmt.Sprintf("loading the manager image on Kind. Arch: %s", arch))
	err := utils.LoadImageToKindClusterWithName(controllerManagerImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager image into Kind")
	err = utils.LoadImageToKindClusterWithName(heirImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the heir image into Kind")
	err = utils.LoadImageToKindClusterWithName(heirTunnelImage)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the plane tunnel image into Kind")
	setupCertManager()
})

var _ = AfterSuite(func() {
	teardownCertManager()
})

// setupCertManager installs CertManager if needed for webhook tests.
// Skips installation if CERT_MANAGER_INSTALL_SKIP=true or if already present.
func setupCertManager() {
	if os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true" {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager installation (CERT_MANAGER_INSTALL_SKIP=true)\n")
		return
	}

	By("checking if CertManager is already installed")
	if utils.IsCertManagerCRDsInstalled() {
		_, _ = fmt.Fprintf(GinkgoWriter, "CertManager is already installed. Skipping installation.\n")
		return
	}

	// Mark for cleanup before installation to handle interruptions and partial installs.
	shouldCleanupCertManager = true

	By("installing CertManager")
	Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
}

// teardownCertManager uninstalls CertManager if it was installed by setupCertManager.
// This ensures we only remove what we installed.
func teardownCertManager() {
	if !shouldCleanupCertManager {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping CertManager cleanup (not installed by this suite)\n")
		return
	}

	By("uninstalling CertManager")
	utils.UninstallCertManager()
}
