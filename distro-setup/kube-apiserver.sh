#!/bin/sh

exec /usr/local/bin/kube-apiserver \
  --allow-privileged=true \
  --authorization-mode=Node,RBAC \
  --bind-address=0.0.0.0 \
  --enable-bootstrap-token-auth=true \
  --client-ca-file=/etc/kubernetes/pki/ca.crt \
  --enable-admission-plugins=NamespaceLifecycle,NodeRestriction,LimitRanger,ServiceAccount,DefaultStorageClass,ResourceQuota \
  --etcd-servers=http://127.0.0.1:2379 \
  --event-ttl=1h \
  --kubelet-certificate-authority=/etc/kubernetes/pki/ca.crt \
  --kubelet-client-certificate=/etc/kubernetes/pki/kube-apiserver.crt \
  --kubelet-client-key=/etc/kubernetes/pki/kube-apiserver.key \
  --runtime-config=api/all=true \
  --service-account-key-file=/etc/kubernetes/pki/service-accounts.crt \
  --service-account-signing-key-file=/etc/kubernetes/pki/service-accounts.key \
  --service-account-issuer=https://kubernetes.default.svc.cluster.local \
  --service-cluster-ip-range=10.96.0.0/12 \
  --service-node-port-range=30000-32767 \
  --tls-cert-file=/etc/kubernetes/pki/kube-apiserver.crt \
  --tls-private-key-file=/etc/kubernetes/pki/kube-apiserver.key \
  --v=2