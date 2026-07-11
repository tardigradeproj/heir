package server

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/broker"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/egress_selector"
	"github.com/tardigradeproj/heir/pkg/tunnel/shrd"
	"github.com/tardigradeproj/outbound"
)

// resolveReplicaCount looks up dnsName and returns the number of addresses
// returned. It returns 1 on any error so agents always receive a valid count.
func resolveReplicaCount(ctx context.Context, dnsName string) int {
	if dnsName == "" {
		return 1
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, dnsName)
	if err != nil {
		log.WithError(err).WithField("dns", dnsName).Warn("replica discovery DNS lookup failed, reporting 1 instance")
		return 1
	}
	if len(addrs) == 0 {
		return 1
	}
	return len(addrs)
}

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

func New(tunnel, egressSel *ListenerConfig, connectionKeepAliveInterval time.Duration, replicaDiscoveryDNS string) (*Server, error) {
	id := uuid.New().String()
	registry := outbound.NewRegistry()
	registry.Register(outbound.Upstream{
		Id:   shrd.IdentityUpstreamID,
		Name: "identity",
		Dial: func(ctx context.Context) (net.Conn, error) {
			local, remote := net.Pipe()
			n := resolveReplicaCount(ctx, replicaDiscoveryDNS)
			payload, _ := json.Marshal(shrd.PlaneTunnelIdentity{Id: id, NumberOfInstances: n})
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
