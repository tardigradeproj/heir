# MasterAgent Redesign: Standalone Per-Tenant Controller

## Summary

`pkg/masteragent` currently runs as a set of independent goroutines (`CSRAutoApprover`,
`SyncKubernetesEndpoints`, `kine`, `readyz`) inside the same container as `kube-apiserver`,
`kube-controller-manager`, and `kube-scheduler`. Per the [architecture overview](https://tardigradeproj.github.io/docs/architecture),
all of these processes run in **one container** under a single S6 Overlay supervision tree —
masteragent is not a separate deployable unit, it is compiled into the same control-plane image
and started as one more supervised process.

CSR approval and Kubernetes-Service endpoint sync are both hand-rolled `time.Ticker` loops that
`List` and diff every 15 seconds, rather than event-driven reconcilers.

This has two problems as masteragent's scope grows (adding a Helm/addon controller, and more):

1. **Release coupling.** Any change to masteragent — a CSR bug fix, a new controller — requires
   rebuilding and rolling the entire control-plane image, restarting the actual `kube-apiserver`
   along with it. There is no way to ship masteragent independently of the control plane it
   supervises.
2. **Replica coupling.** masteragent's concurrency is pinned to
   `Runtime.Spec.DeploymentSpec.Replicas`. When a tenant runs more than one control-plane replica,
   every replica runs its own CSR approver and endpoint syncer, racing to approve the same CSRs
   and update the same `EndpointSlice`. This is masked today by idempotency, but it does not scale
   cleanly as more reconcilers are added.

This document proposes extracting masteragent into its own per-tenant Deployment in the
management cluster, running a single `controller-runtime` `Manager` with one reconciler per
concern.

---

## Goals

* Decouple masteragent's release cadence and crash domain from the control-plane image.
* Move CSR approval and endpoint sync from polling to event-driven reconciliation.
* Provide one consistent foundation (single `Manager`, one `Reconciler` per concern) for adding
  further controllers — starting with a Helm/addon controller — without repeating the
  ticker-and-error-channel pattern in `masteragent.go`.
* Decouple masteragent's replica count from the control-plane pod's replica count, removing the
  N-way CSR/`EndpointSlice` race.
* Preserve the existing per-tenant isolation model: one masteragent per `Runtime`, holding only
  that tenant's own credentials — never a process shared across tenants.

## Non-Goals

* Centralizing CSR approval, endpoint sync, or Helm reconciliation for **all** tenants behind the
  management-cluster operator. This was considered and rejected — see [Alternatives](#alternatives-considered).
* Adopting Flux (`source-controller` + `helm-controller`) or any external GitOps toolchain. The
  Helm controller here is a purpose-built reconciler using the Helm Go SDK directly, not a GitOps
  pipeline.

---

## Proposal

### Component layout

* A new Deployment, one per `Runtime` (e.g. `<runtime-name>-masteragent`), created and owned by
  `RuntimeReconciler` alongside the existing control-plane Deployment, Service, and PKI Secret.
* It runs in the management cluster but reaches the tenant apiserver **over the network**, via the
  tenant's existing `Service`, instead of over loopback.
* It mounts the same **admin** kubeconfig Secret already produced by `setupPKIAuthConfiguration`
  (`layout.Auth.AdminConf`) — the same credential masteragent uses today, just consumed from a
  different pod. No new credential is introduced, and no credential is shared across tenants.
* It runs a single `ctrl.Manager` built from that kubeconfig (pointed at the tenant's `Service`
  DNS name rather than `localhost`), registering one `Reconciler` per concern:
  * `CSRApprovalReconciler` — watches `CertificateSigningRequest`, filtered to
    `kubernetes.io/kubelet-serving`.
  * `KubernetesEndpointReconciler` — watches the worker-profile `ConfigMap`, reconciles the
    `kubernetes` Service `EndpointSlice`; keeps a `RequeueAfter` as a fallback resync interval
    since DNS re-resolution isn't watchable.
  * `HelmReconciler` — watches a `HelmChart`-shaped custom resource (see
    [CRD bootstrap](#crd-bootstrap-on-upstream-clusters) below).
  * Future controllers register the same way.
* `kine` and `readyz` **stay in the control-plane container, behavior unchanged.** Both are tied to
  local process access — `kine` is the storage backend the local apiserver dials over loopback, and
  `readyz` probes `localhost:6443`/`10257`/`10259` directly — neither has a reason to move. They do
  move code-wise, into their own package — see [Code structure](#code-structure) below.

### Replica model

masteragent's replica count is no longer tied to `Spec.DeploymentSpec.Replicas`. Run it as a
single replica by default. If HA is needed later, standard `controller-runtime` leader election
(a `Lease` in the tenant cluster) covers the whole `Manager` at once — no bespoke per-controller
election scheme is needed, since every reconciler shares one `Manager`.

### Startup sequence

1. Build a client from the mounted admin kubeconfig, targeting the tenant's `Service`.
2. Wait for the tenant apiserver to become reachable (retry/backoff on connection refused — this
   is expected both on initial bootstrap and whenever the control-plane pod restarts).
3. Apply masteragent's own CRDs into the tenant cluster (idempotent upsert — see below).
4. Wait for each applied CRD to report `Established`.
5. Start the `Manager`; registered reconcilers begin watching and converging.

---

## CRD bootstrap on upstream clusters

Addon/Helm management is meant to be driven **from the upstream (tenant) cluster's own API**, not
proxied through the management-cluster operator. Concretely: a tenant administrator (or a GitOps
process pointed at that tenant) should be able to edit a `HelmChart`-style custom resource
directly against their own cluster's apiserver and have it converge, exactly as they would with
any other in-cluster controller.

This means the tenant cluster's own CRD registry, not the management cluster's, is what needs to
carry these types. Every tenant cluster is a fresh, independent apiserver at creation time — none
of masteragent's CRDs exist there until something installs them. That "something" is masteragent
itself, as part of its startup sequence:

* The types live in their own API package, `api/masteragent/v1alpha1`, scaffolded the same way as
  `api/v1alpha1` but under a distinct `GroupVersion` (e.g. `masteragent.tardigrade.runtime.io/v1alpha1`)
  and never registered into the management-cluster operator's scheme in `cmd/main.go` — the operator
  never talks to this type.
* `controller-gen` generates the CRD YAML from that package straight into `pkg/masteragent/crd/`
  (a second `manifests` invocation in the Makefile, separate from the `config/crd/bases` one, so this
  CRD is never picked up by the kustomization that installs CRDs into the *management* cluster).
  masteragent embeds that YAML via `go:embed` and applies it to the **tenant** apiserver instead.
* On **every** startup — not only first run — masteragent applies these manifests via
  server-side apply. This makes CRD schema upgrades ship as a side effect of a normal masteragent
  release: bump the image, the next restart re-applies the newer embedded schema. No separate
  migration tooling.
* Before starting the `Manager`, masteragent polls each applied CRD for the `Established`
  condition — a watch registered against a not-yet-established CRD will error.
* Default/day-0 CR instances — the CoreDNS, kube-proxy, and CNI resources the architecture
  overview describes as "bootstrap manifests" applied once the API server is healthy — are applied
  by masteragent at this same step, sourced from values it already has locally (the worker profile
  `ConfigMap`, and whatever `Runtime`-spec-derived config is mirrored into masteragent's own
  config). This replaces today's one-shot bootstrap script with an idempotent apply that the
  `HelmReconciler` can continue to converge afterward.
* **Day-2 changes happen directly against the CR objects in the tenant cluster.** Editing the
  `HelmChart` CR there — by hand, or via a GitOps process running against that tenant — is the
  supported path; masteragent's `HelmReconciler` is already watching that type and converges it.
  The management-cluster operator is not in this path at all once bootstrap has happened.

**Open question:** does the management-cluster operator ever need to push updated values into
these CRs — e.g. propagating a `Runtime` spec change — or is masteragent's local defaulting on
startup sufficient for the addon fields that exist today? This needs a product-level answer before
implementation; it doesn't change the bootstrap mechanism above either way.

### RBAC / trust boundary

The admin kubeconfig masteragent already holds grants everything this design needs — CRD
creation, CSR approval, `EndpointSlice` writes, Helm installs — at the same trust level as today,
just exercised from a separate pod. This design does **not** give the management-cluster operator
any new access to tenant clusters; that was the earlier, rejected "centralize into the operator"
alternative below.

---

## Code structure

**Prerequisite (done):** the project has been migrated to Kubebuilder's multi-group layout
(`multigroup: true` in `PROJECT`), since `api/masteragent/v1alpha1` is a second API group
alongside the existing `controlplane` one and Kubebuilder rejects a second group under
single-group layout. This moved `api/v1alpha1` → `api/controlplane/v1alpha1` and
`internal/controller/*` → `internal/controller/controlplane/*`, with all import paths and the
envtest `suite_test.go` relative CRD/binary paths updated accordingly. `kubebuilder create api
--group masteragent --version v1alpha1 --kind HelmChart` can now be run (declining the
controller-stub prompt, and reverting the auto-inserted `config/crd/kustomization.yaml` line and
any scheme registration in `cmd/main.go`, per the [CRD bootstrap](#crd-bootstrap-on-upstream-clusters)
section above). The `manifests`/`generate` Makefile targets were also scoped from a blanket
`paths="./..."` down to `./api/controlplane/...` and `./internal/controller/controlplane/...`,
fixing a latent bug where those targets broke on the multiple `func main()` declarations across
`cmd/*.go` (each built individually via explicit filename, never as one package) — this was already
broken on `main` before this migration, just never triggered. A third scoped target for
`./api/masteragent/...` (output to `pkg/masteragent/crd/`) still needs to be added once that
package exists.

`cmd/masteragent` is a single binary today (`master-agent`, via `cmd/masteragent/app.go`) that
becomes kine + readyz once baked into the control-plane image. Splitting the reconcilers out means
that binary now does two distinct jobs. Rather than becoming two binaries, it stays one Go module
with two `cobra` subcommands, packaged into **two separate images** — so the two halves can still
ship on independent cadences, which is the point of this whole redesign:

```
api/masteragent/v1alpha1/          # NEW — tenant-facing CRD types (HelmChart, ...)
    groupversion_info.go           # own GroupVersion, e.g. masteragent.tardigrade.runtime.io/v1alpha1
    helmchart_types.go
    zz_generated.deepcopy.go
    # scaffolded the normal way: kubebuilder create api --group masteragent ...
    # NOT added to cmd/main.go's scheme — the operator never talks to this type.

pkg/masteragent/
    crd/                            # generated CRD YAML, output here so go:embed can reach it
        masteragent.tardigrade.runtime.io_helmcharts.yaml
    crdbootstrap.go                 # embed + server-side apply + wait-for-Established
    manager.go                      # builds the ctrl.Manager (tenant kubeconfig, tenant Service host)
    csr_reconciler.go               # CSRApprovalReconciler — was csr_approver.go, reshaped to Reconcile()
    endpoint_reconciler.go          # KubernetesEndpointReconciler — was k8s_endpoint.go
    helm_reconciler.go              # new
    supervisor/                     # what stays in the control-plane container
        kine.go                     # was runKine() in masteragent.go
        readyz.go                   # unchanged, moved as-is
        supervisor.go               # Run(ctx, Config) — today's masteragent.Run(), narrowed to just these two

cmd/masteragent/
    app.go                          # cobra root: `master-agent`
    supervisor_cmd.go               # `master-agent supervisor` — kine+readyz, what images/base/Dockerfile runs
    controller_cmd.go               # `master-agent controller` — ctrl.Manager, what the new Deployment runs

images/
    base/Dockerfile                 # unchanged image, S6 script now calls `master-agent supervisor`
    Dockerfile.masteragent          # NEW, small single-process image running `master-agent controller`
```

A few decisions embedded in this layout:

* **`csr_approver.go`/`k8s_endpoint.go` get rewritten, not just moved.** They're ticker loops
  today; as reconcilers they become `Reconcile(ctx, req) (ctrl.Result, error)` +
  `SetupWithManager`, the same shape as `internal/controller/runtime_controller.go`. That's a real
  rewrite of the control flow, not a file move.
* **Naming split.** "masteragent" is kept as the identity of the new standalone controller
  (CSR/Endpoint/Helm — the job the architecture overview already attributes to MasterAgent), and
  kine+readyz are pulled into a `supervisor` sub-scope since they're the narrower thing left
  behind. This is a naming call, not a structural one — easy to change.
* **`RuntimeReconciler` gains one more `setup*` step** (`setupMasterAgentDeployment`, the same
  shape as `setupPlaneTunnelDeployment`) to create the new per-`Runtime` Deployment and mount the
  existing admin kubeconfig Secret into it, pointed at the tenant `Service` instead of `localhost`.
* **Two images, one Go module.** `images/base/Dockerfile` (unchanged, still bundles
  `kube-apiserver`/`kube-scheduler`/`kube-controller-manager` under S6) only needs to invoke
  `master-agent supervisor` now. `images/Dockerfile.masteragent` is new — a small, single-process
  image (no S6 needed) running `master-agent controller`, referenced by the new Deployment. Because
  they're separate images with independently tagged releases, a controller-image bump doesn't
  require rebuilding or retagging the base image, even though both come from the same source tree.

---

## Alternatives considered

**1. Keep masteragent in the control-plane container, add reconcilers as more goroutines.**
Rejected: no shared informer cache or leader election across controllers, masteragent's
concurrency stays pinned to the control-plane pod's replica count, and every change still ships
via the monolithic control-plane image.

**2. Centralize all tenants behind the management-cluster operator (one shared controller reconciling every tenant).**
Rejected: this was the first version of this proposal and it doesn't hold up. It concentrates
every tenant's live API credentials into one process — a crash, bug, or compromise there affects
every tenant simultaneously, instead of one. It also requires dynamic multi-cluster client
lifecycle management that `controller-runtime`'s single-cluster `Manager` doesn't provide out of
the box: creating and tearing down an informer set per `Runtime` as they're created/deleted,
rebuilding clients when `RegeneratePKILeafCerts` rotates a tenant's certificate, and tolerating one
tenant's apiserver being transiently down without stalling reconciliation for every other tenant
sharing the same workqueue. Per-tenant masteragent avoids all of this for free.

**3. Adopt Flux (`source-controller` + `helm-controller`) for the Helm piece.**
Rejected: Flux's helm-controller is built for a GitOps-operator deployment topology (its own
manager, `HelmRelease` CRDs, a `source-controller` fetching charts) — a heavier and differently
shaped component than "one embedded reconciler inside a per-tenant agent." A direct integration
against the Helm Go SDK (`helm.sh/helm/v3/pkg/action`) is a better fit here.

---

## Rollout / migration

* **New `Runtime`s:** `RuntimeReconciler` creates the masteragent Deployment alongside the
  control-plane Deployment from day one.
* **Existing `Runtime`s:** need a backfill step — `RuntimeReconciler` creates the masteragent
  Deployment for `Runtime`s that don't yet have one. The in-container CSR/endpoint loops
  (`csr_approver.go`, `k8s_endpoint.go`) can only be removed from the control-plane image once
  every live `Runtime` has been migrated; removing them earlier would leave a window where neither
  the in-pod loops nor the new pod are running for a given tenant. This needs a concrete
  sequencing plan (e.g. a version gate or feature flag) before implementation — not resolved by
  this document.
* `kine` and `readyz` are unaffected: no change to the control-plane image beyond eventually
  deleting `csr_approver.go` and `k8s_endpoint.go` once migration is complete.

## Open questions

* HA story for masteragent: is a single replica acceptable, or should it run leader-elected with
  2+ replicas from day one?
* Where do default CR values (CNI choice, CoreDNS version, etc.) originate — hardcoded defaults in
  masteragent, or mirrored from the `Runtime` spec via the operator? (see CRD bootstrap section)
* Migration sequencing for already-running `Runtime`s (see Rollout section above).
* Does the management cluster's network policy need an explicit allow rule for
  masteragent → control-plane `Service` traffic, or is pod-to-pod traffic unrestricted today?
