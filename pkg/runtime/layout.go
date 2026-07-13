package runtime

// MountEntry binds a Secret/ConfigMap key to its absolute mount path inside the control-plane container.
type MountEntry struct {
	// SecretKey is the key name used in the Kubernetes Secret or ConfigMap.
	SecretKey string
	// MountPath is the absolute path where the file will be projected inside the container.
	MountPath string
}

// PKILayout describes the certificate and key entries stored in the <name>-pki Secret.
type PKILayout struct {
	CACert             MountEntry
	CAKey              MountEntry
	APIServerCert      MountEntry
	APIServerKey       MountEntry
	ServiceAccountCert MountEntry
	ServiceAccountKey  MountEntry
}

// AuthLayout describes the kubeconfig entries stored in the <name>-auth Secret.
type AuthLayout struct {
	AdminConf             MountEntry
	ControllerManagerConf MountEntry
	SchedulerConf         MountEntry
}

// StaticManifest describes the manifests to be applied at the moment the cluster is initialized
type StaticManifest struct {
	Coredns     MountEntry
	KubeProxy   MountEntry
	Bootstrap   MountEntry
	NodeProfile MountEntry
	FlannelCNI  MountEntry
}

// ConfigLayout describes the s6-overlay run-script entries stored in the <name>-config ConfigMap.
// Each entry is the run script for one supervised service.
type ConfigLayout struct {
	APIServer         MountEntry
	ControllerManager MountEntry
	Scheduler         MountEntry
}

// ControlPlaneLayout groups all Secret/ConfigMap keys and their container mount paths for a
// control-plane instance. Use NewControlPlaneLayout to obtain the canonical set of values.
type ControlPlaneLayout struct {
	PKI            PKILayout
	Auth           AuthLayout
	Config         ConfigLayout
	StaticManifest StaticManifest
}

// NewControlPlaneLayout returns the fixed layout that describes every file that must be
// projected into the control-plane container: PKI certificates, kubeconfigs, and s6-overlay
// service scripts.
func NewControlPlaneLayout() ControlPlaneLayout {
	return ControlPlaneLayout{
		PKI: PKILayout{
			CACert:             MountEntry{SecretKey: "ca.crt", MountPath: "/etc/kubernetes/pki/ca.crt"},
			CAKey:              MountEntry{SecretKey: "ca.key", MountPath: "/etc/kubernetes/pki/ca.key"},
			APIServerCert:      MountEntry{SecretKey: "apiserver.crt", MountPath: "/etc/kubernetes/pki/apiserver.crt"},
			APIServerKey:       MountEntry{SecretKey: "apiserver.key", MountPath: "/etc/kubernetes/pki/apiserver.key"},
			ServiceAccountCert: MountEntry{SecretKey: "sa.crt", MountPath: "/etc/kubernetes/pki/sa.crt"},
			ServiceAccountKey:  MountEntry{SecretKey: "sa.key", MountPath: "/etc/kubernetes/pki/sa.key"},
		},
		Auth: AuthLayout{
			AdminConf:             MountEntry{SecretKey: "admin.conf", MountPath: "/etc/kubernetes/admin.conf"},
			ControllerManagerConf: MountEntry{SecretKey: "kube-controller-manager.conf", MountPath: "/etc/kubernetes/kube-controller-manager.conf"},
			SchedulerConf:         MountEntry{SecretKey: "kube-scheduler.conf", MountPath: "/etc/kubernetes/kube-scheduler.conf"},
		},
		StaticManifest: StaticManifest{
			Coredns:     MountEntry{SecretKey: "coredns.yaml", MountPath: "/etc/kubernetes/manifests/manifests.d/coredns.yaml"},
			KubeProxy:   MountEntry{SecretKey: "kubeproxy.yaml", MountPath: "/etc/kubernetes/manifests/manifests.d/kubeproxy.yaml"},
			Bootstrap:   MountEntry{SecretKey: "tlsbootstrap.yaml", MountPath: "/etc/kubernetes/manifests/manifests.d/tlsbootstrap.yaml"},
			NodeProfile: MountEntry{SecretKey: "nodeprofile.yaml", MountPath: "/etc/kubernetes/manifests/manifests.d/nodeprofile.yaml"},
			FlannelCNI:  MountEntry{SecretKey: "flannelcni.yaml", MountPath: "/etc/kubernetes/manifests/manifests.d/flannelcni.yaml"},
		},
		Config: ConfigLayout{
			APIServer:         MountEntry{SecretKey: "kube-apiserver.sh", MountPath: "/etc/kubernetes/manifests/kube-apiserver.sh"},
			ControllerManager: MountEntry{SecretKey: "kube-controller-manager.sh", MountPath: "/etc/kubernetes/manifests/kube-controller-manager.sh"},
			Scheduler:         MountEntry{SecretKey: "kube-scheduler.sh", MountPath: "/etc/kubernetes/manifests/kube-scheduler.sh"},
		},
	}
}
