package controlplaneimage

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/build/controlplaneimage/internal/container/docker"
	"github.com/tardigradeproj/heir/pkg/build/controlplaneimage/internal/kube"
	"github.com/tardigradeproj/heir/pkg/version"

	"math/rand"
	"net/url"
	"os"

	"path"
	"runtime"
	"strings"
	"time"

	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
)

const (
	// httpProxy is the HTTP_PROXY environment variable key
	httpProxy = "HTTP_PROXY"
	// httpsProxy is the HTTPS_PROXY environment variable key
	httpsProxy = "HTTPS_PROXY"
	// noProxy is the NO_PROXY environment variable key
	noProxy = "NO_PROXY"
)

type buildContext struct {
	// option fields
	image     string
	baseImage string
	arch      string
	buildType string
	kubeParam string
	// non-option fields
	builder kube.Builder
}

// Build builds a node image using the supplied options
func Build(options ...Option) error {
	// default options
	ctx := &buildContext{
		image:     DefaultImage,
		baseImage: DefaultBaseImage,
		arch:      runtime.GOARCH,
	}

	// apply user options
	for _, option := range options {
		if err := option.apply(ctx); err != nil {
			return err
		}
	}

	// verify that we're using a supported arch
	if !supportedArch(ctx.arch) {
		log.Warnf("unsupported architecture %q", ctx.arch)
	}

	if ctx.buildType == "" {
		ctx.buildType = detectBuildType(ctx.kubeParam)
		if ctx.buildType != "" {
			log.Infof("Detected build type: %q", ctx.buildType)
		} else {
			log.Warn("No build type specified")
			return fmt.Errorf("no build type specified")
		}

	}

	if ctx.buildType == "file" {
		log.Infof("Building using local file: %q", ctx.kubeParam)
		if info, err := os.Stat(ctx.kubeParam); err == nil && info.Mode().IsRegular() {
			builder, err := kube.NewTarballBuilder(ctx.kubeParam)
			if err != nil {
				return err
			}
			ctx.builder = builder
		}
	}

	return ctx.Build()
}

// detectBuildType detect the type of build required based on the param passed in the following order
// url: if the param is a valid http or https url
// file: if the param refers to an existing regular file
// source: if the param refers to an existing directory
// release: if the param is a semantic version expression (does this require the v preprended?
func detectBuildType(param string) string {
	u, err := url.ParseRequestURI(param)
	if err == nil {
		if u.Scheme == "http" || u.Scheme == "https" {
			return "url"
		}
	}
	if info, err := os.Stat(param); err == nil {
		if info.Mode().IsRegular() {
			return "file"
		}
		if info.Mode().IsDir() {
			return "source"
		}
	}
	_, err = version.ParseSemantic(param)
	if err == nil {
		return "release"
	}
	return ""
}

func supportedArch(arch string) bool {
	switch arch {
	default:
		return false
	// currently we nominally support building node images for these
	case "amd64":
	case "arm64":
	}
	return true
}

// Build builds the cluster node image, the source dir must be set on
// the buildContext
func (c *buildContext) Build() (err error) {
	// ensure kubernetes build is up-to-date first
	log.Info("Starting to build Kubernetes")
	bits, err := c.builder.Build()
	if err != nil {
		log.Errorf("Failed to build Kubernetes: %v", err)
		return errors.Wrap(err, "failed to build kubernetes")
	}
	log.Info("Finished building Kubernetes")

	// then perform the actual docker image build
	log.Info("Building node image ...")
	return c.buildImage(bits)
}

func (c *buildContext) buildImage(bits kube.Bits) error {
	// create build container
	// NOTE: we are using docker run + docker commit, so we can install
	// debian packages without permanently copying them into the image.
	// if docker gets proper squash support, we can rm them instead
	// This also allows the KubeBit implementations to programmatically
	// install in the image
	containerID, err := c.createBuildContainer()
	cmder := docker.ContainerCmder(containerID)

	// ensure we will delete it
	if containerID != "" {
		defer func() {
			_ = exec.Command("docker", "rm", "-f", "-v", containerID).Run()
		}()
	}
	if err != nil {
		log.Errorf("Image build Failed! Failed to create build container: %v", err)
		return err
	}

	log.Info("Building in container: " + containerID)

	// copy artifacts in
	for _, binary := range bits.BinaryPaths() {
		// service file expects /usr/bin/kubelet
		nodePath := "/usr/local/bin/" + path.Base(binary)
		log.WithFields(log.Fields{"binary": binary, "node.path": nodePath}).Info("copying binary")
		if err := exec.Command("docker", "cp", binary, containerID+":"+nodePath).Run(); err != nil {
			return err
		}
		if err := cmder.Command("chmod", "+x", nodePath).Run(); err != nil {
			return err
		}
		if err := cmder.Command("chown", "root:root", nodePath).Run(); err != nil {
			return err
		}
	}

	// write version
	// TODO: support grabbing version from a binary instead?
	// This may or may not be a good idea ...
	rawVersion := bits.Version()
	if err := createFile(cmder, "/tardigrade/version", rawVersion); err != nil {
		return err
	}

	// Save the image changes to a new image
	if err = exec.Command(
		"docker", "commit",
		// we need to put this back after changing it when running the image
		"--change", `ENTRYPOINT ["/init"]`,
		// remove proxy settings since they're for the building process
		// and should not be carried with the built image
		"--change", `ENV HTTP_PROXY="" HTTPS_PROXY="" NO_PROXY=""`,
		containerID, c.image,
	).Run(); err != nil {
		log.Errorf("Image build Failed! Failed to save image: %v", err)
		return err
	}

	log.Infof("Image %q build completed.", c.image)
	return nil
}

func (c *buildContext) createBuildContainer() (id string, err error) {
	// attempt to explicitly pull the image if it doesn't exist locally
	// errors here are non-critical; we'll proceed with execution, which includes a pull operation
	_ = docker.Pull(c.baseImage, dockerBuildOsAndArch(c.arch), 4)
	// this should be good enough: a specific prefix, the current unix time,
	// and a little random bits in case we have multiple builds simultaneously
	random := rand.New(rand.NewSource(time.Now().UnixNano())).Int31()
	id = fmt.Sprintf("tardigrade-build-%d-%d", time.Now().UTC().Unix(), random)
	runArgs := []string{
		"-d",                 // make the client exit while the container continues to run
		"--entrypoint=sleep", // the container should hang forever, so we can exec in it
		"--name=" + id,
		"--platform=" + dockerBuildOsAndArch(c.arch),
		"--security-opt", "seccomp=unconfined",
	}
	// pass proxy settings from environment variables to the building container
	// to make them work during the building process
	for _, name := range []string{httpProxy, httpsProxy, noProxy} {
		val := os.Getenv(name)
		if val == "" {
			val = os.Getenv(strings.ToLower(name))
		}
		if val != "" {
			runArgs = append(runArgs, "--env", name+"="+val)
		}
	}
	err = docker.Run(
		c.baseImage,
		runArgs,
		[]string{
			"infinity", // sleep infinitely to keep container running indefinitely
		},
	)
	if err != nil {
		return id, errors.Wrap(err, "failed to create build container")
	}
	return id, nil
}
