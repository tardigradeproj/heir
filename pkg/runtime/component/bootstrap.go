package component

func CreateBootstrapManifest() []byte {
	return []byte(bootstrapManifest)
}

const bootstrapManifest = `
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: samaritano:worker-profile-reader
  namespace: kube-system
  labels:
    managed-by: bootstrap
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["worker-profile"]
    verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: samaritano:worker-profile-reader
  namespace: kube-system
  labels:
    managed-by: bootstrap
subjects:
  - kind: Group
    name: system:nodes
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: samaritano:worker-profile-reader
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: samaritano:kubelet-bootstrap
  labels:
    managed-by: bootstrap
subjects:
  - kind: Group
    name: system:bootstrappers:worker
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: system:node-bootstrapper
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: samaritano:kubelet-bootstrap-auto-approve-csrs
  labels:
    managed-by: bootstrap
subjects:
  - kind: Group
    name: system:bootstrappers:worker
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: system:certificates.k8s.io:certificatesigningrequests:nodeclient
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: samaritano:kubelet-cert-renew
  labels:
    managed-by: bootstrap
subjects:
  - kind: Group
    name: system:nodes
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: system:certificates.k8s.io:certificatesigningrequests:selfnodeclient
  apiGroup: rbac.authorization.k8s.io
`
