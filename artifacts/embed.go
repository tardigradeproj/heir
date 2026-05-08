package artifact

import "embed"

//go:embed worker/kubelet worker/containerd worker/runc worker/containerd-shim-runc-v2 worker/ctr worker/crictl worker/cni
var FS embed.FS

//go:embed all:manifests
var Manifests embed.FS
