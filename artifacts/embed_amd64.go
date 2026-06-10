//go:build amd64 && embedartifacts

package artifact

import "embed"

//go:embed worker/amd64/kubelet.zst worker/amd64/containerd.zst worker/amd64/runc.zst worker/amd64/containerd-shim-runc-v2.zst worker/amd64/ctr.zst worker/amd64/crictl.zst worker/amd64/cni.tar.zst
var FS embed.FS
