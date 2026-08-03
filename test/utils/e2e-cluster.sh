#!/bin/sh
set -o errexit

cat <<EOF | ${KIND} create --name ${KIND_CLUSTER}  cluster --config=-
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