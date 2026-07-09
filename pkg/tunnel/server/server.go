package server

import (
	"time"

	"github.com/tardigradeproj/heir/pkg/tunnel/server/egress_selector"
)

type ListenerConfig struct {
	CertPath   string
	KeyPath    string
	CaCertPath string
	Addr       string
}
type Server struct {
	// tunnel
	tunnel *ListenerConfig
	// egress selector
	egressSelector              *ListenerConfig
	connectionKeepAliveInterval time.Duration

	egressSelectorServer *egress_selector.Server
	tunnelServer         TunnelServer
}

func New(tunnel *ListenerConfig, egressSelector *ListenerConfig) (*Server, error) {
	//srv := &Server{
	//	tunnel: &ListenerConfig{}
	//}
	return nil, nil
}
