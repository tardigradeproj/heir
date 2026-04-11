package artifact

import "embed"

//go:embed worker/kubelet worker/containerd
var FS embed.FS
