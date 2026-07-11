package server

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/broker"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/egress_selector"
	"github.com/tardigradeproj/outbound"
)

// identityUpstreamID is the upstream ID workers dial to retrieve this
// plane-tunnel instance's stable identity.
const identityUpstreamID uint8 = 0

type ListenerConfig struct {
	CertPath   string
	KeyPath    string
	CaCertPath string
	Addr       string
}

type Server struct {
	tunnelServer         *TunnelServer
	egressSelectorServer *egress_selector.Server
}

func New(tunnel, egressSel *ListenerConfig, connectionKeepAliveInterval time.Duration) (*Server, error) {
	id := uuid.New().String()
	registry := outbound.NewRegistry()
	registry.Register(outbound.Upstream{
		Id:   identityUpstreamID,
		Name: "identity",
		Dial: func(ctx context.Context) (net.Conn, error) {
			local, remote := net.Pipe()
			payload, _ := json.Marshal(map[string]string{"id": id})
			go func() {
				_, _ = local.Write(payload)
				_ = local.Close()
			}()
			return remote, nil
		},
	})
	b := broker.New(registry, connectionKeepAliveInterval)

	ts := NewTunnelServer(
		tunnel.CertPath,
		tunnel.KeyPath,
		tunnel.CaCertPath,
		tunnel.Addr,
		b,
	)

	es := egress_selector.New(
		egressSel.Addr,
		egressSel.CertPath,
		egressSel.KeyPath,
		egressSel.CaCertPath,
		b,
	)

	return &Server{
		tunnelServer:         ts,
		egressSelectorServer: es,
	}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)
	go func() { errCh <- s.tunnelServer.Serve(ctx) }()
	go func() { errCh <- s.egressSelectorServer.Serve(ctx) }()

	return <-errCh
}
