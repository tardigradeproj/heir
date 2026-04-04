package controlplaneimage

import (
	"path"
	"regexp"
	"strings"

	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
)

// createFile creates the file at filePath in the container,
// ensuring the directory exists and writing contents to the file
func createFile(containerCmder exec.Cmder, filePath, contents string) error {
	// NOTE: the paths inside the container should use the path package
	// and not filepath (!), we want posixy paths in the linux container, NOT
	// whatever path format the host uses. For paths on the host we use filepath
	if err := containerCmder.Command("mkdir", "-p", path.Dir(filePath)).Run(); err != nil {
		return err
	}

	return containerCmder.Command(
		"cp", "/dev/stdin", filePath,
	).SetStdin(
		strings.NewReader(contents),
	).Run()
}

func findSandboxImage(config string) (string, error) {
	match := regexp.MustCompile(`sandbox_image\s+=\s+"([^\n]+)"`).FindStringSubmatch(config)
	if len(match) < 2 {
		return "", errors.New("failed to parse sandbox_image from config")
	}
	return match[1], nil
}

func dockerBuildOsAndArch(arch string) string {
	return "linux/" + arch
}
