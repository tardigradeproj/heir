//go:build arm64 && embedartifacts

package artifact

import "embed"

//go:embed worker/arm64/kubelet.zst worker/arm64/containerd.zst worker/arm64/runc.zst worker/arm64/containerd-shim-runc-v2.zst worker/arm64/ctr.zst worker/arm64/crictl.zst worker/arm64/cni.tar.zst
var FS embed.FS
