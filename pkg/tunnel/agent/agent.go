package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/avast/retry-go"
	log "github.com/sirupsen/logrus"
	obs "github.com/tardigradeproj/heir/pkg/observability"
	"github.com/tardigradeproj/heir/pkg/tunnel/shrd"
	"github.com/tardigradeproj/heir/pkg/util"
	"github.com/tardigradeproj/outbound"
)

type managedConn struct {
	tunnel   *outbound.Tunnel
	identity *shrd.PlaneTunnelIdentity
}

type connState struct {
	mu           sync.RWMutex
	tracker      map[string]managedConn
	disconnected chan string
	target       int
}

type Agent struct {
	tlsConfig                   *tls.Config
	tunnelServerAddr            string
	kubeletAddr                 string
	connectionKeepAliveInterval time.Duration
	registry                    *outbound.Registry
	conn                        connState
}

func New(
	certPath string,
	keyPath string,
	caCertPath string,
	tunnelServerAddr string,
	kubeletAddr string,

	connectionKeepAliveInterval time.Duration,
) (*Agent, error) {
	tlsConfig, err := util.SetupTLSConfig(certPath, keyPath, caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TLS config: %w", err)
	}
	registry := outbound.NewRegistry()
	registry.Register(outbound.Upstream{
		Id:   shrd.KubeletUpstreamID,
		Name: "kubelet",
		Dial: outbound.TCPUpstream(kubeletAddr),
	})
	return &Agent{
		tlsConfig:                   tlsConfig,
		tunnelServerAddr:            tunnelServerAddr,
		kubeletAddr:                 kubeletAddr,
		connectionKeepAliveInterval: connectionKeepAliveInterval,
		registry:                    registry,
		conn: connState{
			tracker:      make(map[string]managedConn),
			disconnected: make(chan string, 16),
			target:       1,
		},
	}, nil
}

func (a *Agent) Start(ctx context.Context) error {
	return a.connectionManager(ctx)
}

func (a *Agent) establishNewConnection(ctx context.Context, lg *log.Entry) error {
	return retry.Do(func() error {
		tunnel, identity, err := a.connect(ctx)
		if err != nil {
			return err
		}
		a.conn.mu.Lock()
		defer a.conn.mu.Unlock()
		if _, ok := a.conn.tracker[identity.Id]; ok {
			_ = tunnel.Close()
			return fmt.Errorf("already connected to plane tunnel instance %s", identity.Id)
		}
		a.conn.tracker[identity.Id] = managedConn{tunnel: tunnel, identity: identity}
		a.conn.target = identity.NumberOfInstances
		connLg := lg.WithField(obs.PlaneTunnelID, identity.Id)
		go func() {
			for {
				if err := tunnel.Serve(ctx); err != nil {
					if tunnel.IsClosed() || ctx.Err() != nil {
						connLg.WithError(err).Debug("tunnel closed, stopping serve")
						break
					}
					connLg.WithError(err).Warn("transient serve error, retrying")
					continue
				}
				if tunnel.IsClosed() || ctx.Err() != nil {
					break
				}
			}
			_ = tunnel.Close()
			a.conn.mu.Lock()
			delete(a.conn.tracker, identity.Id)
			a.conn.mu.Unlock()
			select {
			case a.conn.disconnected <- identity.Id:
			default:
			}
		}()
		return nil
	}, retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.Attempts(4),
		retry.Delay(3*time.Second),
		retry.MaxDelay(30*time.Second),
		retry.OnRetry(func(attempt uint, err error) {
			lg.WithError(err).WithField("attempt", attempt+1).Debug("connection attempt failed, retrying")
		}))
}

// connectionManager maintains persistent connections to every plane tunnel
// replica. It learns the desired replica count from the NumberOfInstances field
// in each identity response and keeps exactly that many distinct connections
// alive (keyed by the instance Id). Duplicate connections, where the remote
// identity is already tracked, are dropped immediately and retried.
func (a *Agent) connectionManager(ctx context.Context) error {
	lg := log.WithFields(log.Fields{
		obs.Component: "connection-manager",
		obs.Server:    a.tunnelServerAddr,
	})

	for {
		a.conn.mu.RLock()
		current := len(a.conn.tracker)
		target := a.conn.target
		a.conn.mu.RUnlock()

		if current >= target {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case id := <-a.conn.disconnected:
				lg.WithFields(log.Fields{
					obs.PlaneTunnelID: id,
					"target":             target,
				}).Info("connection lost, reconnecting")
			}
			continue
		}

		if err := a.establishNewConnection(ctx, lg); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lg.WithError(err).Warn("connection failed, retrying")
			continue
		}

		a.conn.mu.RLock()
		current = len(a.conn.tracker)
		target = a.conn.target
		a.conn.mu.RUnlock()
		lg.WithFields(log.Fields{
			"connections": current,
			"target":      target,
		}).Info("connection with server has been successfully established")
	}
}

// connect dials the plane tunnel server, establishes an outbound session, and
// fetches the server's identity. The returned tunnel is owned by the caller.
func (a *Agent) connect(ctx context.Context) (*outbound.Tunnel, *shrd.PlaneTunnelIdentity, error) {
	lg := log.WithField(obs.Server, a.tunnelServerAddr)

	conn, err := tls.Dial("tcp", a.tunnelServerAddr, a.tlsConfig)
	if err != nil {
		lg.WithError(err).Error("failed to dial tunnel server")
		return nil, nil, fmt.Errorf("failed to dial tunnel server: %w", err)
	}

	cfg := outbound.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = a.connectionKeepAliveInterval
	cfg.ConnectionWriteTimeout = 10 * time.Second

	session, err := outbound.Client(conn, cfg)
	if err != nil {
		_ = conn.Close()
		lg.WithError(err).Error("failed to create outbound session")
		return nil, nil, fmt.Errorf("failed to create outbound session: %w", err)
	}

	tunnel := outbound.NewTunnel(session, a.registry)

	identity, err := a.fetchIdentity(ctx, tunnel)
	if err != nil {
		_ = tunnel.Close()
		return nil, nil, err
	}

	lg.WithField(obs.PlaneTunnelID, identity.Id).Debug("plane tunnel identity received")
	return tunnel, identity, nil
}

// fetchIdentity dials the identity upstream and reads the server's identity payload.
func (a *Agent) fetchIdentity(ctx context.Context, tunnel *outbound.Tunnel) (*shrd.PlaneTunnelIdentity, error) {
	lg := log.WithField(obs.Server, a.tunnelServerAddr)

	stream, err := tunnel.Dial(ctx, shrd.IdentityUpstreamID)
	if err != nil {
		lg.WithError(err).Error("failed to dial identity upstream")
		return nil, fmt.Errorf("failed to dial identity upstream: %w", err)
	}
	defer stream.Close()

	b, err := io.ReadAll(stream)
	if err != nil {
		lg.WithError(err).Error("failed to read identity response")
		return nil, fmt.Errorf("failed to read identity response: %w", err)
	}

	var identity shrd.PlaneTunnelIdentity
	if err := json.Unmarshal(b, &identity); err != nil {
		lg.WithError(err).Error("failed to unmarshal identity")
		return nil, fmt.Errorf("failed to unmarshal identity: %w", err)
	}
	lg.WithFields(log.Fields{obs.PlaneTunnelID: identity.Id, "plane_tunnel.nrOfInstances": identity.NumberOfInstances}).
		Info("plane tunnel identity received")
	return &identity, nil
}
