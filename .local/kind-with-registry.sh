#!/bin/sh
set -o errexit

script_dir="$(cd "$(dirname "$0")" && pwd)"
kubeconfig_path="${script_dir}/../integration-test/kubeconfig.yaml"

# 1. Create registry container unless it already exists
reg_name='kind-registry'
reg_port='5001'
if [ "$(docker inspect -f '{{.State.Running}}' "${reg_name}" 2>/dev/null || true)" != 'true' ]; then
  docker run \
    -d --restart=always -p "127.0.0.1:${reg_port}:5000" --network bridge --name "${reg_name}" \
    registry:2
fi

# 2. Create kind cluster with containerd registry config dir enabled
#
# NOTE: the containerd config patch is not necessary with images from kind v0.27.0+
# It may enable some older images to work similarly.
# If you're only supporting newer relases, you can just use `kind create cluster` here.
#
# See:
# https://github.com/kubernetes-sigs/kind/issues/2875
# https://github.com/containerd/containerd/blob/main/docs/cri/config.md#registry-configuration
# See: https://github.com/containerd/containerd/blob/main/docs/hosts.md
cat <<EOF | kind create --name integration-test --kubeconfig="${kubeconfig_path}" cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
kubeadmConfigPatches:
  - |-
    kind: ClusterConfiguration
    apiServer:
      certSANs:
        - "host.docker.internal"
        - "kind-control-plane"
        - "kubernetes"
        - "kubernetes.default"
        - "kubernetes.default.svc"
        - "kubernetes.default.svc.cluster.local"
        - "localhost"
        - "10.0.2.2"
        - "10.96.0.1"
        - "172.19.0.2"
        - "127.0.0.1"
        - "control-plane"
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: 30080
  - containerPort: 30081
    hostPort: 30081
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry]
    config_path = "/etc/containerd/certs.d"
EOF

# Generate bastion kubeconfig — same content but server host set to 'control-plane'
# so containers inside the kind network can reach the API server directly.
bastion_kubeconfig_path="${script_dir}/../integration-test/bastion-kubeconfig.yaml"
cp "${kubeconfig_path}" "${bastion_kubeconfig_path}"
api_port=$(kubectl --kubeconfig="${bastion_kubeconfig_path}" config view --raw \
  -o jsonpath='{.clusters[0].cluster.server}' | grep -oE '[0-9]+$')
#kubectl --kubeconfig="${bastion_kubeconfig_path}" config set-cluster kind-integration-test \
#  --server="https://control-plane:6443"

# 4. Add the registry config to the nodes
#
# This is necessary because localhost resolves to loopback addresses that are
# network-namespace local.
# In other words: localhost in the container is not localhost on the host.
#
# We want a consistent name that works from both ends, so we tell containerd to
# alias localhost:${reg_port} to the registry container when pulling images
REGISTRY_DIR="/etc/containerd/certs.d/localhost:${reg_port}"
for node in $(kind get nodes --name integration-test); do
  docker exec "${node}" mkdir -p "${REGISTRY_DIR}"
  cat <<EOF | docker exec -i "${node}" cp /dev/stdin "${REGISTRY_DIR}/hosts.toml"
server = "https://localhost:${reg_port}"

[host."http://${reg_name}:5000"]
  capabilities = ["pull", "resolve"]
EOF
done

# 5. Connect the registry to the cluster network if not already connected
# This allows kind to bootstrap the network but ensures they're on the same network
if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${reg_name}")" = 'null' ]; then
  docker network connect "kind" "${reg_name}"
fi

# 6. Document the local registry
# https://github.com/kubernetes/enhancements/tree/master/keps/sig-cluster-lifecycle/generic/1755-communicating-a-local-registry
cat <<EOF | kubectl --kubeconfig="${kubeconfig_path}" apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${reg_port}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

# 7. Push postgres image to the local registry
docker tag heir-base:v0.0.1 "localhost:${reg_port}/heir-base:v0.0.1"
docker push "localhost:${reg_port}/heir-base:v0.0.1"
docker pull postgres:16
docker tag postgres:16 "localhost:${reg_port}/postgres:16"
docker push "localhost:${reg_port}/postgres:16"

docker tag registry.k8s.io/kas-network-proxy/proxy-server:v0.0.37 "localhost:${reg_port}/proxy-server:v0.0.37"
docker push "localhost:${reg_port}/proxy-server:v0.0.37"
docker tag registry.k8s.io/kas-network-proxy/proxy-agent:v0.0.37 "localhost:${reg_port}/proxy-agent:v0.0.37"
docker push "localhost:${reg_port}/proxy-agent:v0.0.37"

# 8. Provision PostgreSQL (secret, deployment, service) and wait until healthy
kubectl --kubeconfig="${kubeconfig_path}" create secret generic postgres-credentials \
  --from-literal=password=kine-password \
  --from-literal=dsn=postgres://kine:kine-password@postgres.default.svc.cluster.local:5432/kine?sslmode=disable \
  --namespace=default

cat <<EOF | kubectl --kubeconfig="${kubeconfig_path}" apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
      - name: postgres
        image: localhost:${reg_port}/postgres:16
        env:
        - name: POSTGRES_DB
          value: kine
        - name: POSTGRES_USER
          value: kine
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: password
        ports:
        - containerPort: 5432
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: default
spec:
  selector:
    app: postgres
  ports:
  - port: 5432
    targetPort: 5432
EOF

kubectl --kubeconfig="${kubeconfig_path}" wait --for=condition=available deployment/postgres \
  --namespace=default \
  --timeout=120s
