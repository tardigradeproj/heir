# GitHub Issues

---

## 1. Fix CNI Plugin Configuration on Cluster

**Labels:** `bug`, `networking`

### Description

CNI (Container Network Interface) plugin configuration is not working correctly on provisioned clusters. Worker nodes joined via `heir provision worker` fail to have functional pod networking due to misconfigured or missing CNI setup.

### Current Behavior

CNI plugins are extracted and placed during worker provisioning (`pkg/provision/join.go`), but pod networking does not come up correctly after a node joins the cluster.

### Expected Behavior

After a worker node is provisioned and joins the cluster, CNI plugins should be fully configured and pod-to-pod networking should be functional.

### Acceptance Criteria

- [ ] CNI plugin binaries are correctly placed under the expected path
- [ ] CNI configuration files are generated and valid
- [ ] Pods scheduled on provisioned worker nodes can communicate across the cluster
- [ ] Verified against the supported CNI plugin (e.g. flannel, calico, or built-in)

---

## 2. Implement Unit Tests for Token Generation

**Labels:** `testing`, `enhancement`

### Description

The token generation package (`pkg/token/`) currently lacks unit test coverage. Bootstrap tokens are critical for secure worker node joins via Kubernetes TLS bootstrapping, and regressions in this logic could silently break cluster provisioning.

### Scope

- `pkg/token/` — token creation, formatting, and secret generation logic
- `pkg/cmd/token/generate.go` — CLI integration

### Acceptance Criteria

- [ ] Unit tests cover token creation and formatting
- [ ] Unit tests cover Kubernetes Secret generation for bootstrap tokens
- [ ] Edge cases tested: duplicate tokens, invalid inputs, API errors
- [ ] Tests run without a live cluster (mock client or fake client)
- [ ] Coverage report shows meaningful improvement in `pkg/token/`

---

## 3. Implement Tests for `provision worker`

**Labels:** `testing`, `enhancement`

### Description

The `provision worker` command (`pkg/provision/join.go`, `pkg/cmd/provision/worker.go`) contains complex logic for extracting binaries, writing kubeconfigs, configuring TLS, setting up systemd services, and configuring CNI. This logic is currently untested.

### Scope

- Binary extraction and placement
- Kubeconfig generation and writing
- Systemd service setup
- CNI configuration
- CLI flags and argument handling in `pkg/cmd/provision/worker.go`

### Acceptance Criteria

- [ ] Unit tests cover individual provisioning steps using filesystem/exec abstractions or mocks
- [ ] Integration or e2e tests verify end-to-end worker join flow (can use Vagrant/VM environment)
- [ ] Failure scenarios are tested (missing binaries, bad token, unreachable API server)
- [ ] Tests are documented and runnable via `make test`

---

## 4. Relocate Kubernetes Manifests and Artifacts to `/etc/kubernetes/heir`

**Labels:** `enhancement`, `breaking-change`

### Description

Currently, Kubernetes manifests and artifacts (certificates, kubeconfigs, static pod manifests, binaries) are placed directly under `/etc/kubernetes/`. To avoid conflicts with other Kubernetes tooling (e.g. kubeadm) and to clearly namespace Heir-managed files, all paths should be moved under `/etc/kubernetes/heir/`.

### Affected Areas

- `pkg/provision/join.go` — paths for kubeconfig, certs, CNI, binaries
- `distro-setup/` — configuration scripts referencing `/etc/kubernetes/`
- `cmd/distro.go` — bootstrap controller manifest paths
- Any hardcoded paths in `internal/controller/` or `config/`

### Acceptance Criteria

- [ ] All Heir-managed files are written to `/etc/kubernetes/heir/`
- [ ] No files are written directly to `/etc/kubernetes/` by Heir
- [ ] Existing provisioning flow works correctly with new paths
- [ ] Documentation and comments updated to reflect new paths
- [ ] Verified on a fresh Vagrant VM and Kind-based test environment

---

## 5. Improve and Expand Tests for the Controller

**Labels:** `testing`, `enhancement`

### Description

The `RuntimeReconciler` (`internal/controller/runtime_controller.go`) handles PKI generation, kubeconfig creation, and Kubernetes resource management (Deployments, Services, ConfigMaps, Secrets, Ingress). Current test coverage is minimal and does not cover the full reconciliation lifecycle.

### Scope

- Full reconcile loop: creation, update, deletion of `Runtime` resources
- PKI generation and secret population
- Kubeconfig generation
- Deployment/Service/Ingress creation and updates
- Error and retry scenarios
- Status condition updates

### Acceptance Criteria

- [ ] Tests use `envtest` (already in use) with a real API server
- [ ] Each reconcile outcome (create, update, delete) has dedicated test cases
- [ ] PKI secrets are validated for correctness in tests
- [ ] Controller status conditions are asserted
- [ ] Tests are stable and pass consistently in CI

---

## 6. Make Distro Support Resources (ConfigMaps, Secrets) Reusable

**Labels:** `enhancement`, `refactor`

### Description

Resources created to support the distro (ConfigMaps and Secrets for scripts, configuration, and credentials in `internal/controller/`) are tightly coupled to individual `Runtime` instances. Shared configuration that does not vary per cluster (e.g. base distro scripts, common configuration templates) should be extracted and made reusable across multiple `Runtime` resources to reduce duplication and resource bloat.

### Acceptance Criteria

- [ ] Identify which ConfigMaps/Secrets contain per-cluster vs. shared data
- [ ] Shared resources are created once (e.g. at operator startup or in a separate controller) and referenced by `Runtime` resources
- [ ] Per-cluster resources remain scoped to their `Runtime` instance
- [ ] Reconciliation logic handles missing shared resources gracefully
- [ ] No regression in existing provisioning behavior

---

## 7. Implement `create cluster` for Kubernetes (bare-metal/VM) via Heir CLI

**Labels:** `feature`, `cli`

### Description

Add a `heir create cluster` subcommand that provisions a full Kubernetes cluster (control plane + workers) on bare-metal or VM targets using the existing Heir provisioning logic. This command should orchestrate the full lifecycle: bootstrapping the control plane, generating tokens, and joining worker nodes.

### Proposed Interface

```
heir create cluster \
  --name <cluster-name> \
  --control-plane <host> \
  --workers <host1,host2,...> \
  [--version <k8s-version>] \
  [--pod-cidr <cidr>] \
  [--service-cidr <cidr>]
```

### Acceptance Criteria

- [ ] Command scaffolded under `pkg/cmd/create/cluster.go`
- [ ] Provisions control plane using existing operator/controller logic
- [ ] Generates bootstrap token and provisions each worker node
- [ ] Reports cluster status and kubeconfig path on success
- [ ] Handles errors at each step with actionable messages
- [ ] Documented with `--help` output and usage examples

---

## 9. Improve Worker Node Setup: Containerd, CNI, and iptables

**Labels:** `enhancement`, `networking`

### Description

Worker node provisioning (`pkg/provision/worker/join.go`) currently performs a basic setup that is insufficient for a fully functional node. Containerd configuration needs to be hardened, CNI plugins need to be properly installed and configured, and iptables rules need to be established to ensure pod networking and traffic forwarding work correctly after a node joins the cluster.

### Scope

- `pkg/provision/worker/join.go` — containerd config, CNI setup, iptables initialisation
- `pkg/provision/worker/` — any supporting helpers

### Work Items

**Containerd**
- [ ] Review and harden the generated `config.toml` (snapshotter, runtime, cgroup driver)
- [ ] Ensure containerd socket is available and service is healthy before kubelet starts

**CNI**
- [ ] Extract and place CNI plugin binaries under the correct path (`/opt/cni/bin`)
- [ ] Generate a valid CNI configuration file under `/etc/cni/net.d/` compatible with the cluster's CNI provider (Calico or fallback)
- [ ] Verify pod-to-pod and pod-to-service connectivity after node joins

**iptables**
- [ ] Ensure `iptables` (legacy or nft) is available on the worker node
- [ ] Set up required forwarding rules (`net.ipv4.ip_forward`, `bridge-nf-call-iptables`)
- [ ] Validate rules survive a kubelet restart

### Acceptance Criteria

- [ ] A provisioned worker node reaches `Ready` status without manual intervention
- [ ] Pods scheduled on the worker can communicate with pods on other nodes
- [ ] containerd service starts cleanly with the generated configuration
- [ ] iptables forwarding rules are in place after provisioning completes

---

## 8. Implement `create cluster` for Docker via Heir CLI

**Labels:** `feature`, `cli`

### Description

Add support for `heir create cluster --driver docker` (or a dedicated subcommand) that spins up a local Kubernetes cluster using Docker containers as nodes. This is similar to Kind but driven by Heir's own control plane image and provisioning logic, enabling local development and testing workflows without a VM.

### Proposed Interface

```
heir create cluster \
  --driver docker \
  --name <cluster-name> \
  [--workers <count>] \
  [--version <k8s-version>]
```

### Scope

- Leverage the existing `controlplane-image` Docker image build
- Use Docker networking and volume mounts to simulate node environments
- Re-use `pkg/provision/join.go` logic adapted for containerized nodes
- Generate and expose kubeconfig for the created cluster

### Acceptance Criteria

- [ ] Command creates a functional local cluster using Docker
- [ ] Control plane runs as a container using the Heir control plane image
- [ ] Worker nodes are Docker containers that successfully join the cluster
- [ ] Kubeconfig is written locally and usable with `kubectl`
- [ ] `heir delete cluster --driver docker --name <name>` tears down the cluster
- [ ] Works on macOS and Linux (primary dev environments)
- [ ] Documented with `--help` output and usage examples
