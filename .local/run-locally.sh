#!/bin/sh
# Runs steps 1-10 of docs/running-locally.md end to end: builds every heir binary and image,
# stands up the local management cluster, deploys the controller manager, provisions a tenant
# Runtime, joins both dev-worker0 and dev-worker1 to it, and validates both nodes reach Ready.
set -o errexit

# token name:container name pairs, one per worker node.
workers="new-node:dev-worker0 new-node-one:dev-worker1"

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
cd "${repo_root}"

mgmt_kubeconfig="${repo_root}/integration-test/kubeconfig.yaml"
tenant_kubeconfig="${repo_root}/my-cluster-kubeconfig"

echo "==> [1/10] Downloading dependency artifacts and building every heir binary and image"
make build-bins

echo "==> [2/10] Standing up the local management cluster"
./.local/setup-management-cluster.sh
export KUBECONFIG="${mgmt_kubeconfig}"

echo "==> [3/10] Installing the CRDs and deploying the controller manager"
make deploy IMG="localhost:5001/heir-controller-manager:latest"

echo "==> [4/10] Provisioning a tenant Runtime"
kubectl apply -f .local/heir.yaml
echo "    waiting for Runtime/my-cluster to become Available..."
kubectl wait --for=condition=Available runtime/my-cluster --timeout=300s

echo "==> [5/10] Fetching the tenant cluster's kubeconfig"
kubectl get secret my-cluster-kubeconfig -o jsonpath='{.data.kubeconfig}' | base64 -d > "${tenant_kubeconfig}"
kubectl --kubeconfig "${tenant_kubeconfig}" config set-cluster my-cluster --server=https://127.0.0.1:30080

echo "==> [6/10] Issuing a worker join token for each worker node"
kubectl --kubeconfig "${tenant_kubeconfig}" apply -f .local/heir-worker-join-token.yaml
for pair in ${workers}; do
  token_name="${pair%%:*}"
  echo "    waiting for WorkerJoinToken/${token_name} to become Ready..."
  kubectl --kubeconfig "${tenant_kubeconfig}" wait --for=condition=Ready "workerjointoken/${token_name}" --timeout=60s
done

for pair in ${workers}; do
  token_name="${pair%%:*}"
  container_name="${pair##*:}"

  echo "==> [7/10] Copying the join token onto ${container_name}"
  kubectl --kubeconfig "${tenant_kubeconfig}" -n kube-system get secret "${token_name}-jointoken" -o jsonpath='{.data.jointoken}' \
    | docker exec -i "${container_name}" sh -c 'cat > /tmp/token'

  echo "==> [8/10] Joining ${container_name}"
  docker exec "${container_name}" heir provision worker --token="$(docker exec "${container_name}" cat /tmp/token)"

  echo "==> [9/10] Letting ${container_name} pull images from the local registry"
  docker exec "${container_name}" sh -c 'cat >> /etc/lib/heir/containerd/config.toml <<EOF

[plugins."io.containerd.grpc.v1.cri".registry.mirrors."kind-registry:5000"]
  endpoint = ["http://kind-registry:5000"]
EOF
pkill -x containerd'
done

echo "==> [10/10] Validating that both nodes joined"
for pair in ${workers}; do
  container_name="${pair##*:}"
  node_name="${container_name#dev-}"
  kubectl --kubeconfig "${tenant_kubeconfig}" wait --for=condition=Ready "node/${node_name}" --timeout=300s
done
kubectl --kubeconfig "${tenant_kubeconfig}" get nodes

echo "==> Done. Tenant kubeconfig written to ${tenant_kubeconfig}"
