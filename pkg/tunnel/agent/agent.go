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
	"github.com/tardigradeproj/heir/pkg/util"
	"github.com/tardigradeproj/outbound"
)

const (
	identityUpstreamID uint8 = 0
	kubeletUpstreamID  uint8 = 1
)

type managedConn struct {
	tunnel   *outbound.Tunnel
	identity *PlaneTunnelIdentity
}
type PlaneTunnelIdentity struct {
	Id                string `json:"id"`
	NumberOfInstances int    `json:"NumberOfInstances"`
}

type Agent struct {
	tlsConfig                   *tls.Config
	tunnelServerAddr            string
	kubeletAddr                 string
	connectionKeepAliveInterval time.Duration
	registry                    *outbound.Registry
	connTracker                 map[string]managedConn
	mu                          sync.RWMutex
	disconnected                chan string
	target                      int
}

func New(
	certPath string,
	keyPath string,
	caCertPath string,
	serverAddr string,
	kubeletAddr string,

	connectionKeepAliveInterval time.Duration,
) (*Agent, error) {
	tlsConfig, err := util.SetupTLSConfig(certPath, keyPath, caCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to setup TLS config: %w", err)
	}
	registry := outbound.NewRegistry()
	registry.Register(outbound.Upstream{
		Id:   kubeletUpstreamID,
		Name: "kubelet",
		Dial: outbound.TCPUpstream(kubeletAddr),
	})
	return &Agent{
		tlsConfig:                   tlsConfig,
		tunnelServerAddr:            serverAddr,
		kubeletAddr:                 kubeletAddr,
		connectionKeepAliveInterval: connectionKeepAliveInterval,
		registry:                    registry,
		connTracker:                 make(map[string]managedConn),
		disconnected:                make(chan string, 16),
		target:                      1,
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
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.connTracker[identity.Id]; ok {
			_ = tunnel.Close()
			return fmt.Errorf("already connected to plane tunnel instance %s", identity.Id)
		}
		a.connTracker[identity.Id] = managedConn{tunnel: tunnel, identity: identity}
		a.target = identity.NumberOfInstances
		connLg := lg.WithField("plane_tunnel.id", identity.Id)
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
			a.mu.Lock()
			delete(a.connTracker, identity.Id)
			a.mu.Unlock()
			select {
			case a.disconnected <- identity.Id:
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
// identity is already tracked, are dropped immediately and retried. All
// retries use exponential backoff.
func (a *Agent) connectionManager(ctx context.Context) error {
	lg := log.WithFields(log.Fields{
		"component": "connection-manager",
		"server":    a.tunnelServerAddr,
	})

	for {
		a.mu.RLock()
		current := len(a.connTracker)
		target := a.target
		a.mu.RUnlock()

		if current >= target {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case id := <-a.disconnected:
				lg.WithFields(log.Fields{
					"plane_tunnel.id": id,
					"target":          target,
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

		a.mu.RLock()
		current = len(a.connTracker)
		target = a.target
		a.mu.RUnlock()
		lg.WithFields(log.Fields{
			"connections": current,
			"target":      target,
		}).Info("connection established")
	}
}

// connect dials the plane tunnel server, establishes an outbound session, and
// fetches the server's identity. The returned tunnel is owned by the caller.
func (a *Agent) connect(ctx context.Context) (*outbound.Tunnel, *PlaneTunnelIdentity, error) {
	lg := log.WithField("server", a.tunnelServerAddr)

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

	lg.WithField("plane_tunnel.id", identity.Id).Debug("plane tunnel identity received")
	return tunnel, identity, nil
}

// fetchIdentity dials the identity upstream and reads the server's identity payload.
func (a *Agent) fetchIdentity(ctx context.Context, tunnel *outbound.Tunnel) (*PlaneTunnelIdentity, error) {
	lg := log.WithField("server", a.tunnelServerAddr)

	stream, err := tunnel.Dial(ctx, identityUpstreamID)
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

	var identity PlaneTunnelIdentity
	if err := json.Unmarshal(b, &identity); err != nil {
		lg.WithError(err).Error("failed to unmarshal identity")
		return nil, fmt.Errorf("failed to unmarshal identity: %w", err)
	}

	return &identity, nil
}
