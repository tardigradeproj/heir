# Running heir locally

This guide walks through building every heir component from source and wiring them together on
your own machine: a local management cluster (via kind), the heir controller manager, a tenant
`Runtime`, and a worker node (via Vagrant) joined to that tenant cluster. It is the fastest way to
exercise a change end to end before opening a PR.

The project is driven by two Makefiles. `Makefile` covers the heir controller manager itself
(building, testing, installing CRDs, deploying to a cluster). `Makefile.distro` covers everything
the tenant control plane and worker nodes need at runtime, such as the embedded `kubelet`,
`containerd`, and the `masteragent`/`tunnel` binaries, plus the container images built from them.

## Prerequisites

You will need the following tools installed and available on your `PATH`:

1. **Go**, matching the version in `go.mod`, to build the controller manager and the `distro` CLI.
2. **[goreleaser](https://goreleaser.com/)**, which cross-compiles every heir binary and builds
   the tenant control plane and plane tunnel container images.
3. **Docker**, to run the local kind cluster and registry, and because goreleaser uses it to
   build every heir image (including the controller manager).
4. **[kind](https://kind.sigs.k8s.io/)**, to run the local management cluster.
5. **kubectl**, to interact with both the management cluster and, later, the tenant cluster.
6. **[Vagrant](https://www.vagrantup.com/)**, to run a disposable VM that plays the role of a
   worker node, since heir worker nodes are not designed to run as containers.
7. **[yq](https://github.com/mikefarah/yq)**, used by `Makefile.distro` to read dependency
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
(plus a Postgres image) into the registry, and finally provisions a Postgres deployment that the
tenant Runtime will use as its kine storage backend.

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
`30081` so the Vagrant worker can reach them, and uses the Postgres instance from step 2 as its
kine storage backend. Applying it and watching its status will tell you once the tenant control
plane is up.

```sh
kubectl apply -f .local/heir.yaml
kubectl get runtime my-cluster --watch
```

Wait for the `Available` condition to report `True` before moving on.

## 5. Issue a worker join token

A `WorkerJoinToken` mints a short-lived bootstrap token and kubeconfig against a specific tenant
`Runtime`, so that a worker node can authenticate to it during the join process.
`.local/heir-worker-join-token.yaml` requests one for the `my-cluster` Runtime created in the
previous step.

```sh
kubectl apply -f .local/heir-worker-join-token.yaml
kubectl get workerjointoken new-node
```

## 6. Copy the join token onto the Vagrant worker

The controller publishes the minted token as the `jointoken` key of a `<name>-jointoken` Secret in
the management cluster. The `distro provision worker` command, run inside the VM in the next step,
expects that value written to `/home/vagrant/heir/token`. The command below reads the Secret, and
writes it into the running Vagrant VM in a single pipeline.

```sh
kubectl get secret new-node-jointoken -o jsonpath='{.data.jointoken}' \
  | vagrant ssh -c "sudo tee /home/vagrant/heir/token > /dev/null"
```

## 7. Build the distro binary and join the worker

If this is your first time using the VM, start and provision it first with `make vagrant-up`. Then,
from inside the VM, build the `distro` CLI with the `embedartifacts` build tag, which bakes the
worker binaries downloaded in step 1 into the resulting binary, and run its `provision worker`
command with the token copied over in step 6.

```sh
vagrant ssh -c "cd /home/vagrant/heir && go build -tags=embedartifacts cmd/distro.go"
vagrant ssh -c "cd /home/vagrant/heir && sudo ./distro provision worker --token=\$(cat /home/vagrant/heir/token)"
```

If you would rather work interactively, `make vagrant-ssh` opens a shell in the VM, from where you
can run the same two commands (`cd /home/vagrant/heir`, then the `go build` and `sudo ./distro ...` lines above)
directly.

## 8. Validate that the node joined

The Runtime controller also publishes its own admin kubeconfig as a `<name>-kubeconfig` Secret.
Fetching, decoding, and using it lets you query the tenant cluster directly and confirm the worker
node registered successfully.

```sh
kubectl get secret my-cluster-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > my-cluster-kubeconfig
kubectl --kubeconfig my-cluster-kubeconfig get nodes
```

The server address baked into that kubeconfig is `.local/heir.yaml`'s
`controlPlaneExternalEndpoint.apiServer.host`, `10.0.2.2`, the address the *Vagrant worker* uses to
reach the host machine, not an address the host machine itself can dial. If you are running the
command above directly on your host (rather than from inside the Vagrant VM or another container
on the kind network), point it at `localhost` instead, since kind also maps the same NodePort
(`30080`) onto your host:

```sh
kubectl --kubeconfig my-cluster-kubeconfig config set-cluster my-cluster --server=https://localhost:30080
kubectl --kubeconfig my-cluster-kubeconfig get nodes
```



A successful join looks like this, with the Vagrant node in the `Ready` state:

```
NAME             STATUS   ROLES    AGE   VERSION
vagrant-ubuntu   Ready    <none>   42m   v1.xx.xx
```

## Troubleshooting

If the node does not appear, or does not reach `Ready`, open a shell in the VM with
`make vagrant-ssh` and inspect the heir worker service:

```sh
sudo systemctl status heir.service
sudo journalctl -xu heir --since "1 minutes ago"
sudo /var/lib/heir/bin/crictl --runtime-endpoint /run/heir/containerd.sock ps
```

To reset the VM's worker state and try again from step 7, run `.local/cleanup-worker.sh` inside
the VM. It stops the heir service, tears down containerd and its containers, removes the CNI and
iptables state left behind, and deletes every file heir wrote under `/etc`, `/var`, and `/run`.
