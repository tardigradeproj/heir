//go:build arm64 && embedartifacts

package artifact

import "embed"

//go:embed worker/arm64/kubelet worker/arm64/containerd worker/arm64/runc worker/arm64/containerd-shim-runc-v2 worker/arm64/ctr worker/arm64/crictl worker/arm64/cni
var FS embed.FS
