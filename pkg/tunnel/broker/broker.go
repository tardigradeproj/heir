package broker

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/outbound"
)

type Tunnel struct {
	id     string
	tunnel *outbound.Tunnel
	cnn    net.Conn
}

type tunnelSet struct {
	conns []*Tunnel
	rr    uint64 // round-robin cursor
}

type Broker struct {
	mu                          sync.RWMutex
	nodes                       map[string]*tunnelSet
	registry                    *outbound.Registry
	connectionKeepAliveInterval time.Duration
}

func New(registry *outbound.Registry, connectionKeepAliveInterval time.Duration) *Broker {
	return &Broker{
		registry:                    registry,
		connectionKeepAliveInterval: connectionKeepAliveInterval,
		nodes:                       make(map[string]*tunnelSet),
	}
}

func (b *Broker) Register(ctx context.Context, nodeName string, cnn net.Conn) (string, error) {
	id := uuid.New().String()

	cfg := outbound.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = b.connectionKeepAliveInterval
	cfg.ConnectionWriteTimeout = 10 * time.Second

	session, err := outbound.Server(cnn, cfg)
	if err != nil {
		log.WithError(err).Warn("failed to start outbound session")
		_ = cnn.Close()
		return "", fmt.Errorf("failed to start outbound session: %w", err)
	}

	tunnel := outbound.NewTunnel(session, b.registry)
	t := &Tunnel{id: id, tunnel: tunnel, cnn: cnn}

	// register under the lock, create the set if this is the node's first conn
	b.mu.Lock()
	set := b.nodes[nodeName]
	if set == nil {
		set = &tunnelSet{}
		b.nodes[nodeName] = set
	}
	set.conns = append(set.conns, t)
	b.mu.Unlock()

	logEntry := log.WithFields(log.Fields{"node.name": nodeName, "conn.id": id})
	logEntry.Debug("tunnel registered")

	// This goroutine owns the connection's whole life: serve streams until the
	// session dies or ctx is cancelled, then unregister and close. Serve blocks;
	// it only returns for good once the session is closed.
	go func() {
		defer func() {
			b.Unregister(nodeName, id)
			_ = tunnel.Close() // idempotent; tears down session + cnn
			logEntry.Debug("tunnel unregistered")
		}()

		for {
			if err := tunnel.Serve(ctx); err != nil {
				// If the session is closed OR ctx is done, this is terminal —
				// stop looping and let the defer clean up. Otherwise it was a
				// transient per-accept error; keep serving.
				if tunnel.IsClosed() || ctx.Err() != nil {
					logEntry.WithError(err).Debug("tunnel closed, stopping serve")
					return
				}
				logEntry.WithError(err).Warn("transient serve error, retrying")
				continue
			}
			// Serve returned nil without the session being closed — nothing to
			// accept right now; loop and block again rather than spin.
			if tunnel.IsClosed() || ctx.Err() != nil {
				return
			}
		}
	}()

	return id, nil
}

func (b *Broker) Unregister(nodeName string, id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.nodes[nodeName]
	if set == nil {
		return
	}
	for i, t := range set.conns {
		if t.id == id {
			last := len(set.conns) - 1
			set.conns[i] = set.conns[last]
			set.conns[last] = nil
			set.conns = set.conns[:last]
			break
		}
	}
	if len(set.conns) == 0 {
		delete(b.nodes, nodeName)
	}
}

func (b *Broker) Pick(nodeName string) *Tunnel {
	b.mu.RLock()
	defer b.mu.RUnlock()
	set := b.nodes[nodeName]
	if set == nil || len(set.conns) == 0 {
		return nil
	}
	n := len(set.conns)
	start := atomic.AddUint64(&set.rr, 1)
	for i := 0; i < n; i++ {
		t := set.conns[(start+uint64(i))%uint64(n)]
		if !t.tunnel.IsClosed() {
			return t
		}
	}
	return nil
}
