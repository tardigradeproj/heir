package component

import (
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	heir "github.com/tardigradeproj/heir"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type buildInChart struct {
	enabled bool
	chart   clusteragentv1alpha1.HelmChart
	values  func(runtime *controlplanev1alpha1.Runtime) clusteragentv1alpha1.ValuesSpec
}

const defaultCorednsValues = `
replicaCount: 1
`

func flannelValues(runtime *controlplanev1alpha1.Runtime) clusteragentv1alpha1.ValuesSpec {
	return clusteragentv1alpha1.ValuesSpec{
		Content: fmt.Sprintf(flannelValuesTemplate, runtime.Spec.Cluster.Network.PodCIDR),
	}
}

const flannelValuesTemplate = `
flannel:
  podCidr: %q
  nodeSelector:
    kubernetes.io/os: linux
  healthz:
    port: 0
`

var buildInAddons = map[string]buildInChart{
	"coredns": {
		enabled: true,
		chart: clusteragentv1alpha1.HelmChart{
			TypeMeta: v1.TypeMeta{
				Kind:       "HelmChart",
				APIVersion: clusteragentv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: v1.ObjectMeta{
				Name:      "coredns",
				Namespace: "kube-system",
			},
			Spec: clusteragentv1alpha1.HelmChartSpec{
				Chart: clusteragentv1alpha1.ChartSpec{
					Name:            "coredns",
					Repo:            "https://coredns.github.io/helm",
					Version:         heir.Dependencies.CorednsChart,
					TargetNamespace: "kube-system",
				},
				Runtime: clusteragentv1alpha1.RuntimeOptions{
					Bootstrap: true,
				},
				Helm: clusteragentv1alpha1.HelmOptions{
					BackOffLimit: new(int32(100)),
					Atomic:       true,
					Timeout:      v1.Duration{Duration: time.Minute * 10},
				},
				Values: clusteragentv1alpha1.ValuesSpec{
					Content: defaultCorednsValues,
				},
			},
		},
	},
	"flannel": {
		enabled: true,
		values:  flannelValues,
		chart: clusteragentv1alpha1.HelmChart{
			TypeMeta: v1.TypeMeta{
				Kind:       "HelmChart",
				APIVersion: clusteragentv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: v1.ObjectMeta{
				Name:      "flannel",
				Namespace: "kube-flannel",
			},
			Spec: clusteragentv1alpha1.HelmChartSpec{
				Chart: clusteragentv1alpha1.ChartSpec{
					Name:            "flannel",
					Repo:            "https://flannel-io.github.io/flannel/",
					Version:         heir.Dependencies.FlannelChart,
					CreateNamespace: true,
					TargetNamespace: "kube-flannel",
				},
				Runtime: clusteragentv1alpha1.RuntimeOptions{
					Bootstrap: true,
				},
				Helm: clusteragentv1alpha1.HelmOptions{
					BackOffLimit: new(int32(100)),
					Atomic:       true,
					Timeout:      v1.Duration{Duration: time.Minute * 10},
				},
			},
		},
	},
}

// withValues returns a copy of chart with its Helm values replaced by values.
func withValues(chart clusteragentv1alpha1.HelmChart, values clusteragentv1alpha1.ValuesSpec) clusteragentv1alpha1.HelmChart {
	chart.Spec.Values = values
	return chart
}

// installedChart wraps a user-declared AddonSpec.Install spec into the HelmChart object
// clusteragent creates for it.
func installedChart(name string, spec clusteragentv1alpha1.HelmChartSpec) clusteragentv1alpha1.HelmChart {
	return clusteragentv1alpha1.HelmChart{
		TypeMeta: v1.TypeMeta{
			Kind:       "HelmChart",
			APIVersion: clusteragentv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
		},
		Spec: spec,
	}
}

// ConsolidateAddons builds the full set of HelmCharts Heir should install for runtime.
func ConsolidateAddons(runtime *controlplanev1alpha1.Runtime) ([]clusteragentv1alpha1.HelmChart, error) {
	helmAddons := runtime.Spec.Cluster.Addons.Helm

	declared := make(map[string]controlplanev1alpha1.AddonChartSpec, len(helmAddons.Charts))
	for _, c := range helmAddons.Charts {
		declared[c.Name] = c
	}

	charts := make([]clusteragentv1alpha1.HelmChart, 0, len(buildInAddons)+len(declared))

	for name, buildIn := range buildInAddons {
		addon, isDeclared := declared[name]

		if isDeclared && addon.Install != nil {
			// A full install spec replaces the built-in default entirely.
			charts = append(charts, installedChart(name, *addon.Install))
			continue
		}
		if isDeclared && addon.Enabled != nil && !*addon.Enabled {
			// Removed: excluded from the returned array.
			log.WithField("chart", name).Debug("addon disabled")
			continue
		}
		if !buildIn.enabled {
			continue
		}

		chart := buildIn.chart
		if buildIn.values != nil {
			chart = withValues(chart, buildIn.values(runtime))
		}
		if isDeclared && addon.Override != nil {
			// Only replace the built-in's own default values when the user actually
			// declared an override
			log.WithFields(log.Fields{
				"chart":  name,
				"values": addon.Override.Values,
			}).Debug("addon had values overwritten")
			chart = withValues(chart, addon.Override.Values)
		}
		charts = append(charts, chart)
	}

	for name, addon := range declared {
		if _, isBuiltIn := buildInAddons[name]; isBuiltIn {
			continue // already handled above
		}
		if addon.Install == nil {
			continue
		}
		charts = append(charts, installedChart(name, *addon.Install))
	}

	if image := helmAddons.Runtime.Image; image != "" {
		for i := range charts {
			charts[i].Spec.Runtime.Image = image
		}
	}

	log.WithField("len", len(charts)).Info("addons to be installed")
	return charts, nil
}
