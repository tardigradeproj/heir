package proxy

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
	"inet.af/tcpproxy"
)

// Downstream is a local TCP listener that forwards every accepted connection
// to a linked Upstream. The upstream can be attached or swapped at runtime
// via Link(). If no upstream is linked when a connection arrives, it is
// refused immediately.
//
// The actual TCP forwarding (accept, bidirectional copy, half-close) is
// handled by inet.af/tcpproxy — Downstream adds the dynamic upstream
// resolution layer on top.
type Downstream struct {
	name       string
	listenAddr string
	upstream   atomic.Pointer[Upstream]
	proxy      *tcpproxy.Proxy
}

// NewDownstream creates a named listener bound to listenAddr.
// It does NOT start accepting yet — call Run for that.
// upstream may be nil; link one later with Link().
func NewDownstream(name, listenAddr string, upstream *Upstream) *Downstream {
	d := &Downstream{
		name:       name,
		listenAddr: listenAddr,
	}
	if upstream != nil {
		d.upstream.Store(upstream)
	}
	return d
}

// Name returns the downstream's identifier.
func (d *Downstream) Name() string { return d.name }

// Link attaches or replaces the upstream that this downstream forwards to.
// Safe to call while Run is active. New connections use the new upstream;
// in-flight connections continue to their original backend.
func (d *Downstream) Link(u *Upstream) {
	old := d.upstream.Swap(u)

	oldName := "<none>"
	if old != nil {
		oldName = old.Name()
	}
	newName := "<none>"
	if u != nil {
		newName = u.Name()
	}
	log.WithFields(log.Fields{
		"downstream": d.name,
		"old":        oldName,
		"new":        newName,
	}).Info("downstream linked to upstream")
}

// Unlink removes the upstream. New connections will be refused until a
// new upstream is linked.
func (d *Downstream) Unlink() {
	d.Link(nil)
}

// Upstream returns the currently linked upstream, or nil.
func (d *Downstream) Upstream() *Upstream {
	return d.upstream.Load()
}

// Run starts accepting connections and blocks until ctx is cancelled.
func (d *Downstream) Run(ctx context.Context) error {
	d.proxy = &tcpproxy.Proxy{}

	d.proxy.AddRoute(d.listenAddr, &tcpproxy.DialProxy{
		// Addr is unused because we override DialContext, but tcpproxy
		// logs it on errors so we set it for observability.
		Addr: d.name,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			upstream := d.upstream.Load()
			if upstream == nil {
				return nil, fmt.Errorf("downstream %q: no upstream linked", d.name)
			}
			return upstream.DialContext(ctx, network)
		},
		OnDialError: func(src net.Conn, err error) {
			log.WithFields(log.Fields{
				"downstream": d.name,
				"src":        src.RemoteAddr(),
			}).WithError(err).Error("failed routing to backend")
			src.Close()
		},
	})

	logger := log.WithFields(log.Fields{
		"downstream": d.name,
		"addr":       d.listenAddr,
	})

	if err := d.proxy.Start(); err != nil {
		return fmt.Errorf("downstream %q: %w", d.name, err)
	}
	logger.Info("listening")

	<-ctx.Done()
	logger.Info("shutting down")
	return d.proxy.Close()
}
