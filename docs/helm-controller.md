# HelmChart Controller

Step by step plan for `HelmChartReconciler.Reconcile` (`internal/controller/clusteragent/helmchart_controller.go`),
based on the `HelmChartSpec` fields defined in `api/clusteragent/v1alpha1/helmchart_types.go` and described in
[docs/addons.md](./addons.md). Follows the same shape as `WorkerJoinTokenReconciler`
(`internal/controller/clusteragent/workerjointoken_controller.go`): fetch, finalizer, reconcile, status conditions.

1. **Fetch and handle not found.** Already scaffolded: `r.Get`, swallow `apierrors.IsNotFound`.

2. **Handle deletion first.** If `DeletionTimestamp` is set and the finalizer is present, run an uninstall job
   (`helm uninstall` against `Chart.TargetNamespace`) before removing the finalizer — otherwise deleting the
   `HelmChart` object leaves the release orphaned on the upstream cluster. Mirror `workerjointoken`'s
   `reconcileDelete`: best effort, log on failure, remove the finalizer regardless so a broken upstream cluster
   doesn't block deletion forever.

3. **Ensure the finalizer is present** on first reconcile (same `controllerutil.AddFinalizer` + `Update` +
   early return pattern as `workerjointoken_controller.go:86-94`), since step 2 depends on it.

4. **Resolve the chart source** from `Spec.Chart`: either `Name` + `Repo` + `Version` (install from a repository)
   or `Content` (base64 `.tgz`, decoded and mounted into the job). These are mutually exclusive per the doc
   table — `Content` overrides `Name` per the field comment. Validate that at least one is set.

5. **Materialize values.** Combine `Spec.Values.Content` (raw YAML) and `Spec.Values.Set` (map) into a Secret or
   ConfigMap mounted into the job. `Set` entries take precedence, matching the doc's ordering
   (`--set`/`--set-string` take precedence over `--values`).

6. **CreateOrUpdate the ServiceAccount** for the job, owned by the `HelmChart` via `ctrl.SetControllerReference`
   (same as the join token Secret in `mint`).

7. **CreateOrUpdate the ClusterRoleBinding** binding that ServiceAccount to `cluster-admin` (per the TODO left
   in the scaffold). Needed because the job installs arbitrary charts into `Chart.TargetNamespace`, which may
   not exist yet (see `CreateNamespace`).

8. **Build and reconcile the Job**, translating `HelmChartSpec` into the job's env/args, matching the CLI flag
   column in `docs/addons.md`:
   - image: `Spec.Runtime.Image` (default `ghcr.io/tardigradeproj/helmer:latest`)
   - `--namespace`, `--create-namespace`, `--version`, `--repo`, `--insecure-skip-tls-verify`, `--timeout`,
     `--set`/`--values` from the resolved values
   - `HELM_VERSION` env from `Spec.Helm.Version`
   - `backoffLimit: *Spec.Helm.BackOffLimit`, `activeDeadlineSeconds` from `Spec.Helm.Timeout`
   - `securityContext: Spec.Runtime.SecurityContext` on the pod template
   - Jobs are immutable, so a spec or values change needs a **new** Job — suffix the Job name with a hash of
     the resolved chart plus values (same idea as the checksum drift check in `secretDrifted`), and clean up
     the previous one.

9. **Set owner references** from `HelmChart` to the ServiceAccount, ClusterRoleBinding, Job, and values Secret,
   so deleting the `HelmChart` garbage-collects them (`.Owns(...)` in `SetupWithManager`, same as
   `.Owns(&corev1.Secret{})` in the join token controller). `HelmChart` is cluster scoped and the Job/SA/Secret
   are namespaced — owner refs across that boundary are fine for garbage collection, but `ctrl.SetControllerReference`
   needs the right scheme registration (already done via `AddToScheme`).

10. **Reflect Job status into `HelmChart.Status.Conditions`** via `meta.SetStatusCondition`, same helper used in
    `workerjointoken_controller.go`:
    - Job running: `Progressing`
    - Job succeeded: `Ready=True`
    - Job failed and retries under `BackOffLimit` exhausted: `Degraded`, requeue with backoff (`setDegraded`
      pattern, returns a non-nil error so controller-runtime backs off)

11. **`Bootstrap` is not reconciler logic.** `Spec.Runtime.Bootstrap` is read by the cluster agent's startup
    path (`pkg/clusteragent`) to decide which `HelmChart` objects to apply before anything else, same as
    `ApplyCRDs` today. The reconciler treats bootstrap charts no differently once the object exists.

12. **`SetupWithManager`**: add `.Owns(&batchv1.Job{})`, `.Owns(&corev1.ServiceAccount{})`, and RBAC markers for
    `jobs`, `serviceaccounts`, `clusterrolebindings`, `secrets`/`configmaps` — the current scaffold only has
    RBAC markers for `helmcharts` itself.
