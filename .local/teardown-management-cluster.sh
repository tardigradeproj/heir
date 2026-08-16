#!/bin/sh
set -o errexit

script_dir="$(cd "$(dirname "$0")" && pwd)"
kubeconfig_path="${script_dir}/../integration-test/kubeconfig.yaml"
cluster_name='integration-test'
reg_name='kind-registry'

# 1. Delete the kind cluster
if kind get clusters 2>/dev/null | grep -q "^${cluster_name}$"; then
  kind delete cluster --name "${cluster_name}" --kubeconfig "${kubeconfig_path}"
fi

# 2. Remove kubeconfig files
rm -f "${kubeconfig_path}"
rm -f "${script_dir}/../integration-test/bastion-kubeconfig.yaml"

# 3. Stop and remove the registry container
if docker inspect "${reg_name}" > /dev/null 2>&1; then
  docker stop "${reg_name}" || true
  docker rm "${reg_name}"
fi
