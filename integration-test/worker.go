package integration

import (
	"path/filepath"

	"github.com/k0sproject/bootloose/pkg/cluster"
	"github.com/k0sproject/bootloose/pkg/config"
)

func provisionWorkerNode(name string, nodeName string, workerDir string) (*config.Config, *cluster.Cluster, error) {
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
