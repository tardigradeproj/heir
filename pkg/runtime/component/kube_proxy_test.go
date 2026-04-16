package component

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
)

func kubeProxyRuntime(spec controlplanev1alpha1.KubeProxySpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			UpstreamCluster: controlplanev1alpha1.UpstreamCluster{
				APIServer: controlplanev1alpha1.APIServerSpec{
					ExternalAddress: "https://api.example.com:6443",
				},
				Network: controlplanev1alpha1.NetworkSpec{
					PodCIDR:   "10.244.0.0/16",
					KubeProxy: &spec,
				},
			},
		},
	}
}

func TestGetConfig_Disabled(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{Disabled: true})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil config when disabled, got %+v", cfg)
	}
}

func TestGetConfig_BasicFields(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		Disabled: false,
		RegisterSetting: controlplanev1alpha1.RegistrySettings{
			Registry:   "registry.k8s.io",
			Image:      "kube-proxy:v1.34.0",
			PullPolicy: corev1.PullIfNotPresent,
		},
		Mode:               "iptables",
		MetricsBindAddress: "0.0.0.0:10249",
	})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if cfg.ClusterCIDR != "10.244.0.0/16" {
		t.Errorf("ClusterCIDR: got %q, want %q", cfg.ClusterCIDR, "10.244.0.0/16")
	}
	if cfg.ControlPlaneEndpoint != "https://api.example.com:6443" {
		t.Errorf("ControlPlaneEndpoint: got %q, want %q", cfg.ControlPlaneEndpoint, "https://api.example.com:6443")
	}
	if cfg.Image != "registry.k8s.io/kube-proxy:v1.34.0" {
		t.Errorf("Image: got %q, want %q", cfg.Image, "registry.k8s.io/kube-proxy:v1.34.0")
	}
	if cfg.PullPolicy != string(corev1.PullIfNotPresent) {
		t.Errorf("PullPolicy: got %q, want %q", cfg.PullPolicy, corev1.PullIfNotPresent)
	}
	if cfg.Mode != "iptables" {
		t.Errorf("Mode: got %q, want %q", cfg.Mode, "iptables")
	}
	if cfg.MetricsBindAddress != "0.0.0.0:10249" {
		t.Errorf("MetricsBindAddress: got %q, want %q", cfg.MetricsBindAddress, "0.0.0.0:10249")
	}
	if cfg.DualStack {
		t.Error("expected DualStack to be false")
	}
}

func TestGetConfig_DefaultArgs(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasConfig := false
	hasHostname := false
	for _, arg := range cfg.Args {
		if arg == "--config=/var/lib/kube-proxy/config.conf" {
			hasConfig = true
		}
		if arg == "--hostname-override=$(NODE_NAME)" {
			hasHostname = true
		}
	}
	if !hasConfig {
		t.Errorf("expected --config arg in %v", cfg.Args)
	}
	if !hasHostname {
		t.Errorf("expected --hostname-override arg in %v", cfg.Args)
	}
}

func TestGetConfig_ExtraArgs(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		ExtraArgs: map[string]string{
			"v":          "4",
			"kubeconfig": "/custom/path",
		},
	})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argSet := make(map[string]bool, len(cfg.Args))
	for _, arg := range cfg.Args {
		argSet[arg] = true
	}

	if !argSet["--v=4"] {
		t.Errorf("expected --v=4 in args %v", cfg.Args)
	}
	if !argSet["--kubeconfig=/custom/path"] {
		t.Errorf("expected --kubeconfig=/custom/path in args %v", cfg.Args)
	}
}

func TestGetConfig_ExtraArgs_OverrideDefault(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		ExtraArgs: map[string]string{
			"config": "/custom/config.conf",
		},
	})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	argSet := make(map[string]bool, len(cfg.Args))
	for _, arg := range cfg.Args {
		argSet[arg] = true
	}

	if !argSet["--config=/custom/config.conf"] {
		t.Errorf("expected overridden --config arg in %v", cfg.Args)
	}
	if argSet["--config=/var/lib/kube-proxy/config.conf"] {
		t.Errorf("default --config should have been overridden, but still present in %v", cfg.Args)
	}
}

func TestGetConfig_NodePortAddresses(t *testing.T) {
	addrs := []string{"192.168.0.0/24", "10.0.0.0/8"}
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		NodePortAddresses: addrs,
	})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want, _ := json.Marshal(addrs)
	if cfg.NodePortAddresses != string(want) {
		t.Errorf("NodePortAddresses: got %q, want %q", cfg.NodePortAddresses, string(want))
	}
}

func TestGetConfig_JSONFields(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{})

	cfg, err := getConfig(runtime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each JSON field must be valid JSON (not empty string)
	for name, val := range map[string]string{
		"IPTables":         cfg.IPTables,
		"IPVS":             cfg.IPVS,
		"NFTables":         cfg.NFTables,
		"NodePortAddresses": cfg.NodePortAddresses,
	} {
		if !json.Valid([]byte(val)) {
			t.Errorf("%s is not valid JSON: %q", name, val)
		}
	}
}
