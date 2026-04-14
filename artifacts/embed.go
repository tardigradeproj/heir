package artifact

import "embed"

//go:embed worker/kubelet worker/containerd worker/runc worker/containerd-shim-runc-v2 worker/ctr worker/crictl
var FS embed.FS
