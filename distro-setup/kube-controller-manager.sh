#!/bin/sh

exec /usr/local/bin/kube-controller-manager \
  --bind-address=0.0.0.0 \
  --cluster-cidr=10.200.0.0/16 \
  --allocate-node-cidrs=true \
  --cluster-name=tardigrade \
  --cluster-signing-cert-file=/etc/kubernetes/pki/ca.crt \
  --cluster-signing-key-file=/etc/kubernetes/pki/ca.key \
  --kubeconfig=/etc/kubernetes/kube-controller-manager.conf \
  --root-ca-file=/etc/kubernetes/pki/ca.crt \
  --service-account-private-key-file=/etc/kubernetes/pki/service-accounts.key \
  --service-cluster-ip-range=10.32.0.0/24 \
  --use-service-account-credentials=true \
  --controllers=*,tokencleaner /
  --allocate-node-cidrs=true /
  --v=2

