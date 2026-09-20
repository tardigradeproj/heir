# Running heir locally

This guide walks through building every heir component from source and wiring them together on
your own machine: a local management cluster (via kind), the heir controller manager, a tenant
`Runtime`, and a worker node (`dev-worker0`, a Docker container defined in
`.local/docker-compose.yml`) joined to that tenant cluster. It is the fastest way to exercise a
change end to end before opening a PR.

The project is driven by two Makefiles. `Makefile` covers the heir controller manager itself
(building, testing, installing CRDs, deploying to a cluster). `Makefile.distro` covers everything
the tenant control plane and worker nodes need at runtime, such as the embedded `kubelet`,
`containerd`, and the `masteragent`/`tunnel` binaries, plus the container images built from them.

## Prerequisites

You will need the following tools installed and available on your `PATH`:

1. **Go**, matching the version in `go.mod`, to build the controller manager and the `distro` CLI.
2. **[goreleaser](https://goreleaser.com/)**, which cross-compiles every heir binary and builds
   the tenant control plane and plane tunnel container images.
3. **Docker**, with the `docker compose` plugin, to run the local kind cluster and registry, the
   `dev-worker0` container that plays the role of a worker node, and because goreleaser uses it
   to build every heir image (including the controller manager).
4. **[kind](https://kind.sigs.k8s.io/)**, to run the local management cluster.
5. **kubectl**, to interact with both the management cluster and, later, the tenant cluster.
6. **[yq](https://github.com/mikefarah/yq)**, used by `Makefile.distro` to read dependency
   versions out of `.dependencies-version.yaml`.

## 1. Download dependency artifacts and build every heir binary and image

heir embeds the processes it runs, such as `kubelet` and `containerd`, inside its own binaries, so 
these need to be downloaded and built before anything else. Running the
command below invokes goreleaser in snapshot mode, which downloads every worker and control-plane
dependency for the current architecture, cross-compiles the `distro`, `tunnel`, `masteragent`, and
`manager` binaries, and builds and tags the tenant control plane image
(`ghcr.io/tardigradeproj/heir`), the plane tunnel image (`ghcr.io/tardigradeproj/heir-tunnel`), and
the controller manager image (`ghcr.io/tardigradeproj/heir-controller-manager`) locally.

```sh
make build-bins
```

## 2. Stand up the local management cluster

`.local/setup-management-cluster.sh` creates the environment the rest of this guide runs against.
It starts a local Docker registry container, creates a kind cluster configured to trust that
registry, tags and pushes the `heir`/`heir-tunnel`/`heir-controller-manager` images built in step 1
(plus a Postgres image) into the registry, starts the worker containers defined in
`.local/docker-compose.yml` (this guide only uses `dev-worker0`; `dev-worker1` is available if you
need a second node), and finally provisions a Postgres deployment that the tenant Runtime will use
as its kine storage backend.

```sh
./.local/setup-management-cluster.sh
```

The script writes kubeconfig under `integration-test/bastion-kubeconfig.yaml`, which you use from
your host machine, and `bastion-kubeconfig.yaml`, which points at an address reachable from inside
the kind Docker network and is what the tenant control plane itself is configured to use. For the
rest of this guide, export the host-facing one:

```sh
export KUBECONFIG="$(pwd)/integration-test/kubeconfig.yaml"
```

## 3. Install the CRDs and deploy the controller manager

`make deploy` renders the `config/default` kustomization, which bundles the CRDs, RBAC, and the
controller manager Deployment together, and applies all of it in one shot. Passing `IMG` points the
Deployment at the image you pushed to the local registry in step 2, rather than the default.

```sh
make deploy IMG="localhost:5001/heir-controller-manager:latest"
```

## 4. Provision a tenant Runtime

`.local/heir.yaml` is a sample `Runtime` that references the `heir` and `heir-tunnel` images
pushed to the local registry, exposes the API server and plane tunnel on NodePorts `30080` and
`30081` so `dev-worker0` can reach them over the `kind` docker network both it and the kind
control-plane node are attached to, and uses the Postgres instance from step 2 as its kine storage
backend. Applying it and watching its status will tell you once the tenant control plane is up.

```sh
kubectl apply -f .local/heir.yaml
kubectl describe runtime my-cluster 
```

Wait for the `Available` condition to report `True` before moving on.

## 5. Fetch the tenant cluster's kubeconfig

`WorkerJoinToken` is reconciled by `clusteragent`, which runs against the tenant cluster itself
rather than the management cluster your `$KUBECONFIG` currently points at, so every command from
here through the join needs to target the tenant cluster instead. The Runtime controller publishes
an admin kubeconfig for it as a `<name>-kubeconfig` Secret in the management cluster; fetch and
decode it once, up front.

```sh
kubectl get secret my-cluster-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > my-cluster-kubeconfig
```

The server address baked into that kubeconfig is `.local/heir.yaml`'s
`controlPlaneExternalEndpoint.apiServer.host`, `integration-test-control-plane` — the kind
control-plane container's own name, reachable from `dev-worker0` because both containers sit on
the same `kind` docker network, but not an address the host machine itself can dial. Since the
rest of this guide runs the `--kubeconfig my-cluster-kubeconfig` commands directly on your host,
point it at `localhost` instead, since kind also maps the same NodePort (`30080`) onto your host:

```sh
kubectl --kubeconfig my-cluster-kubeconfig config set-cluster my-cluster --server=https://127.0.0.1:30080
```

## 6. Issue a worker join token

A `WorkerJoinToken` mints a short-lived bootstrap token against a specific tenant
`Runtime`, so that a worker node can authenticate to it during the join process. Since
`clusteragent` reconciles it on the tenant cluster itself, apply and inspect it there using the
kubeconfig fetched in the previous step. `.local/heir-worker-join-token.yaml` requests one for the
`my-cluster` Runtime created earlier.

```sh
kubectl --kubeconfig my-cluster-kubeconfig apply -f .local/heir-worker-join-token.yaml
kubectl --kubeconfig my-cluster-kubeconfig get workerjointoken new-node
```

## 7. Copy the join token onto dev-worker0

`clusteragent` publishes the minted token as the `jointoken` key of a `<name>-jointoken` Secret in
the `kube-system` namespace of the tenant cluster. The `heir provision worker` command, run inside
`dev-worker0` in the next step, expects that value written to `/tmp/token`. The command below reads
the Secret and writes it into the running container in a single pipeline.

```sh
kubectl --kubeconfig my-cluster-kubeconfig -n kube-system get secret new-node-jointoken -o jsonpath='{.data.jointoken}' \
  | docker exec -i dev-worker0 sh -c 'cat > /tmp/token'
```

## 8. Join the worker

Connect worker node using the token copied in step 7.

```sh
docker exec dev-worker0 heir provision worker --token="$(docker exec dev-worker0 cat /tmp/token)"
```

If you would rather work interactively, `docker exec -it dev-worker0 bash` opens a shell in the
container, from where you can run `heir provision worker --token=$(cat /tmp/token)` directly.

## 9. Let dev-worker0 pull images from the local registry

Any image referenced as `kind-registry:5000/<name>` fails to pull from `dev-worker0`, since
containerd's CRI plugin assumes HTTPS for every registry host except the literal name `localhost`,
and the `registry:2` container behind `kind-registry:5000` only serves plain HTTP. `heir provision
worker` does not yet support configuring a registry mirror itself, so patch the containerd config
it generated by hand and restart just the containerd process (not the whole `heir` service, which
would regenerate the file and undo this) to pick up the change:

```sh
docker exec dev-worker0 sh -c 'cat >> /etc/lib/heir/containerd/config.toml <<EOF

[plugins."io.containerd.grpc.v1.cri".registry.mirrors."kind-registry:5000"]
  endpoint = ["http://kind-registry:5000"]
EOF
pkill -x containerd'
```

`heir`'s own process supervisor restarts containerd automatically after the `pkill`, using the
same `--config` path, so it picks up the appended mirror. Repeat this any time you rejoin
`dev-worker0` from scratch, since `provision worker` rewrites `config.toml` on every run.

## 10. Validate that the node joined

Using the tenant kubeconfig fetched back in step 5, confirm the worker node registered
successfully.

```sh
kubectl --kubeconfig my-cluster-kubeconfig get nodes
```

A successful join looks like this, with `worker0` (dev-worker0's hostname) in the `Ready` state:

```
NAME      STATUS   ROLES    AGE   VERSION
worker0   Ready    <none>   42m   v1.xx.xx
```

## Troubleshooting

If the node does not appear, or does not reach `Ready`, open a shell in the container with
`docker exec -it dev-worker0 bash` and inspect the heir worker service:

```sh
systemctl status heir.service
journalctl -xu heir --since "1 minutes ago"
/var/lib/heir/bin/crictl --runtime-endpoint /run/heir/containerd.sock ps
```

`docker exec` already drops you in as root inside the container, so none of these need `sudo`.

To reset the container's worker state and try again from step 8, pipe `.local/cleanup-worker.sh`
into a shell running inside it (the container has no copy of the repo of its own, so this avoids
needing one):

```sh
cat .local/cleanup-worker.sh | docker exec -i dev-worker0 sh
```

It stops the heir service, tears down containerd and its containers, removes the CNI and iptables
state left behind, and deletes every file heir wrote under `/etc`, `/var`, and `/run`.
