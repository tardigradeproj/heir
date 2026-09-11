# Provisioning Cluster Addons

## Background

Today, addons such as CoreDNS, the CNI plugin, nodeProfile, and others are provisioned using
Kubernetes manifests defined in YAML files. Because these manifests are rendered from scratch,
the `Runtime` CRD has grown bespoke fields to expose the handful of settings each component
needs, for example `NetworkSpec.Coredns` (`CorednsSpec.Replicas`, `RegistrySettings`,
`ClusterDNSIP` in `api/v1alpha1/coredns.go`) and `NetworkSpec.CNI`.

As more addons are added, this CRD surface will keep growing.

## Helm Based Addons

To address this, the manifest rendering approach is being replaced by a Helm controller.
Addons ship with default Helm configuration and values, and users can override those values
as needed. The cluster agent is configured to apply all bootstrap Helm charts on startup, and
it installs the chart CRD on the upstream cluster at the same time.

By default, Heir boots the cluster with kube proxy, RKE2, Flannel CNI and more, each installed
as a Helm chart. Users can disable any of these through the `Runtime` manifest:

```yaml
kind: Runtime
metadata:
  name: my-cluster
  namespace: default
spec:
  controlPlane: ...
  cluster:
    addons:
      coredns:
        enabled: false
```

To add an entirely new chart to the bootstrap phase, or to override an existing addon's
configuration, set `install` for a new addon or `override` for an existing one:

```yaml
kind: Runtime
metadata:
  name: my-cluster
  namespace: default
spec:
  controlPlane: ...
  cluster:
    addons:
      cilium: # user defined addon
        install:
          ...
      metrics-server: # override addon default value
        override:
          values: |
            replicas: 2
```

## Chart Controller

The Helm controller is part of the cluster agent. It watches `HelmChart` resources and creates the
Kubernetes `Job`s responsible for installing the corresponding Helm charts. Those Job pods run
on the upstream cluster.

### Chart Spec Fields

<table>
<thead>
<tr><th>Field</th><th>Default</th><th>Description</th><th>Equivalent CLI Flag</th></tr>
</thead>
<tbody>
<tr><td><code>spec.chart.name</code></td><td></td><td>Helm chart name in the repository, or a complete HTTPS URL to a chart archive (.tgz)</td><td><code>CHART</code></td></tr>
<tr><td><code>spec.chart.targetNamespace</code></td><td><code>default</code></td><td>Namespace the Helm chart is installed into</td><td><code>--namespace</code></td></tr>
<tr><td><code>spec.chart.createNamespace</code></td><td><code>false</code></td><td>Create the target namespace if it does not already exist</td><td><code>--create-namespace</code></td></tr>
<tr><td><code>spec.chart.version</code></td><td></td><td>Helm chart version to install (when installing from a repository)</td><td><code>--version</code></td></tr>
<tr><td><code>spec.chart.repo</code></td><td></td><td>Helm chart repository URL</td><td><code>--repo</code></td></tr>
<tr><td><code>spec.helm.version</code></td><td><code>v3</code></td><td>Helm version to use (<code>v2</code> or <code>v3</code>)</td><td></td></tr>
<tr><td><code>spec.helm.insecureSkipTLSVerify</code></td><td><code>false</code></td><td>Skip TLS certificate checks when downloading the chart</td><td><code>--insecure-skip-tls-verify</code></td></tr>
<tr><td><code>spec.helm.backOffLimit</code></td><td><code>10</code></td><td>Number of retries allowed before the job is considered failed</td><td></td></tr>
<tr><td><code>spec.helm.timeout</code></td><td><code>300s</code></td><td>Timeout for Helm operations, expressed as a duration string (<code>300s</code>, <code>10m</code>, <code>1h</code>, etc.)</td><td><code>--timeout</code></td></tr>
<tr><td><code>spec.runtime.bootstrap</code></td><td><code>false</code></td><td>Set to <code>true</code> if this chart is required to bootstrap the cluster (CoreDNS, kube proxy, etc.)</td><td></td></tr>
<tr><td><code>spec.runtime.image</code></td><td></td><td>Image used to run the Helm job, for example <code>rancher/klipper-helm:v0.3.0</code></td><td></td></tr>
<tr><td><code>spec.runtime.securityContext</code></td><td></td><td>Custom <code>v1.PodSecurityContext</code> applied to the Helm job pod</td><td></td></tr>
<tr><td><code>spec.values.set</code></td><td></td><td>Override simple chart values; these take precedence over <code>spec.values.content</code></td><td><code>--set</code> / <code>--set-string</code></td></tr>
<tr><td><code>spec.values.content</code></td><td></td><td>Override complex chart values via inline YAML content</td><td><code>--values</code></td></tr>
</tbody>
</table>

### Example

```yaml
spec:
  chart:
    name: "my-chart"
    content: "YmFzZ..."
    repo: "https://charts.example.com"
    version: "1.2.3"
    targetNamespace: "default"
    createNamespace: false

  helm:
    version: "v3"
    insecureSkipTLSVerify: false
    backOffLimit: 10
    timeout: "300s"
    failurePolicy: "reinstall"

  runtime:
    bootstrap: false
    image: "rancher/klipper-helm:v0.3.0"
    securityContext: {}

  values:
    set:
      key1: "value1"
    content: |-
      complexNode:
        enabled: true
```
