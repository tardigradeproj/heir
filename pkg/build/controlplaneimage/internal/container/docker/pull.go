package docker

import (
	"time"

	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/kind/pkg/exec"
)

// Pull pulls an image, retrying up to retries times
func Pull(image string, platform string, retries int) error {
	log.Debugf("Pulling image: %s for platform %s ...", image, platform)
	err := exec.Command("docker", "pull", "--platform="+platform, image).Run()
	// retry pulling up to retries times if necessary
	if err != nil {
		for i := 0; i < retries; i++ {
			time.Sleep(time.Second * time.Duration(i+1))
			log.Debugf("Trying again to pull image: %q ... %v", image, err)
			// TODO(bentheelder): add some backoff / sleep?
			err = exec.Command("docker", "pull", "--platform="+platform, image).Run()
			if err == nil {
				break
			}
		}
	}
	return err
}
