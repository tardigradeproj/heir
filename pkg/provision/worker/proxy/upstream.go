package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	defaultDialTimeout = 8 * time.Second
)

// Upstream is a named pool of backend endpoints. Connections are attempted
// in order — the first successful dial wins. The pool can be updated
// atomically at runtime; in-flight connections are unaffected.
type Upstream struct {
	mu        sync.RWMutex
	name      string
	endpoints []Endpoint
}

// Endpoint is a single backend target.
type Endpoint struct {
	Host string
	Port int
}

// Addr returns host:port.
func (e Endpoint) Addr() string {
	return net.JoinHostPort(e.Host, fmt.Sprintf("%d", e.Port))
}

// NewUpstream creates a named upstream with initial endpoints.
func NewUpstream(name string, endpoints []Endpoint) *Upstream {
	return &Upstream{
		name:      name,
		endpoints: endpoints,
	}
}

// Name returns the upstream's identifier.
func (u *Upstream) Name() string { return u.name }

// Update atomically replaces the endpoint list. New connections use the
// updated list; in-flight connections keep their original backend.
func (u *Upstream) Update(endpoints []Endpoint) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.endpoints = endpoints

	addrs := make([]string, len(endpoints))
	for i, ep := range endpoints {
		addrs[i] = ep.Addr()
	}
	log.WithFields(log.Fields{
		"upstream": u.name,
		"backends": addrs,
	}).Debug("upstream updated")
}

// Endpoints returns a snapshot of the current endpoint list.
func (u *Upstream) Endpoints() []Endpoint {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]Endpoint, len(u.endpoints))
	copy(out, u.endpoints)
	return out
}

// DialContext tries each endpoint in order and returns the first successful
// connection. This is the method that downstreams call to reach backends.
func (u *Upstream) DialContext(ctx context.Context, network string) (net.Conn, error) {
	u.mu.RLock()
	eps := make([]Endpoint, len(u.endpoints))
	copy(eps, u.endpoints)
	u.mu.RUnlock()

	if len(eps) == 0 {
		return nil, fmt.Errorf("upstream %q: no endpoints configured", u.name)
	}

	dialer := &net.Dialer{Timeout: defaultDialTimeout}
	var lastErr error
	for _, ep := range eps {
		addr := ep.Addr()
		conn, err := dialer.DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}
		log.WithFields(log.Fields{
			"upstream": u.name,
			"endpoint": addr,
		}).WithError(err).Debug("dial failed, trying next")
		lastErr = err
	}
	return nil, fmt.Errorf("upstream %q: all endpoints exhausted: %w", u.name, lastErr)
}
