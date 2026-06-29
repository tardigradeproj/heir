package broker

import (
	"net/http"

	"github.com/tardigradeproj/heir/pkg/tunnel/server/leases"
)

type Kubelet struct {
	dst leases.AgentLeaseIndex
}

func (t *Kubelet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// find the right server instance
	// forward to kubelet via persistent connection
}
