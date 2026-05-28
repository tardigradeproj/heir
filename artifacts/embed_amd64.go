//go:build amd64 && embedartifacts

package artifact

import "embed"

//go:embed worker/amd64/kubelet worker/amd64/containerd worker/amd64/runc worker/amd64/containerd-shim-runc-v2 worker/amd64/ctr worker/amd64/crictl worker/amd64/cni
var FS embed.FS
