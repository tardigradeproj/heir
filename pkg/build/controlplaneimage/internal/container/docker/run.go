package docker

import (
	"sigs.k8s.io/kind/pkg/exec"
)

// Run creates a container with "docker run", with some error handling
func Run(image string, runArgs []string, containerArgs []string) error {
	// construct the actual docker run argv
	args := []string{"run"}
	args = append(args, runArgs...)
	args = append(args, image)
	args = append(args, containerArgs...)
	cmd := exec.Command("docker", args...)
	return cmd.Run()
}
