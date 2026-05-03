// cgroup validation
package sys

import (
	"os"
	"os/exec"

	log "github.com/sirupsen/logrus"
	"k8s.io/component-helpers/node/util/sysctl"
)

func loadKernelModule(moduleName string) {
	if _, err := os.Stat("/sys/module/" + moduleName); err == nil {
		log.Info("Module " + moduleName + " was already loaded")
		return
	}

	if err := exec.Command("modprobe", "--", moduleName).Run(); err != nil {
		log.WithError(err).Warnf("Failed to load kernel module %v with modprobe", moduleName)
	}
}

func Configure() error {
	loadKernelModule("overlay")
	loadKernelModule("nf_conntrack")
	loadKernelModule("br_netfilter")
	loadKernelModule("iptable_nat")
	loadKernelModule("iptable_filter")
	loadKernelModule("nft-expr-counter")
	loadKernelModule("nfnetlink-subsys-11")
	loadKernelModule("nft-chain-2-nat")

	sysctls := map[string]int{
		"net/ipv4/conf/all/forwarding":       1,
		"net/ipv4/conf/default/forwarding":   1,
		"net/bridge/bridge-nf-call-iptables": 1,
	}
	sys := sysctl.New()
	for entry, value := range sysctls {
		if val, _ := sys.GetSysctl(entry); val != value {
			log.Infof("Set sysctl '%v' to %v", entry, value)
			if err := sys.SetSysctl(entry, value); err != nil {
				log.Errorf("Failed to set sysctl: %v", err)
			}
		}
	}
	return nil
}
