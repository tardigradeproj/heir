#!/usr/bin/env bash

set -euo pipefail

export KUBECONFIG=/etc/kubernetes/admin.conf
MANIFEST_DIR="/etc/kubernetes/manifests.d"

wait_for_apiserver() {
  # Use readyz and -f (fail silently on HTTP errors)
  until curl -skf https://127.0.0.1:6443/readyz > /dev/null; do
    echo "[bootstrap] waiting for API server to be ready..."
    sleep 2
  done
}

reconcile_all() {
  if [ -d "$MANIFEST_DIR" ]; then
    echo "[bootstrap] reconciling full state from $MANIFEST_DIR"
    kubectl apply -f "$MANIFEST_DIR" \
      --prune -l managed-by=bootstrap || echo "[bootstrap] ERROR: Failed to apply some manifests."
  else
    echo "[bootstrap] WARNING: Directory $MANIFEST_DIR not found."
  fi
}

main() {
  wait_for_apiserver

  # Continuous reconciliation loop
  while true; do
    reconcile_all
    sleep 60
  done
}

main