package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"

	log "github.com/sirupsen/logrus"
)

// dsHandle tracks a running downstream's goroutine so it can be
// individually stopped and awaited.
type dsHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager is a registry of upstreams and downstreams. It allows adding
// either in any order and linking them together on demand.
type Manager struct {
	mu          sync.RWMutex
	upstreams   map[string]*Upstream
	downstreams map[string]*Downstream
	handles     map[string]*dsHandle

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager creates an empty manager. Call Close to release resources.
func NewManager(ctx context.Context) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{
		upstreams:   make(map[string]*Upstream),
		downstreams: make(map[string]*Downstream),
		handles:     make(map[string]*dsHandle),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// AddUpstream registers a named upstream. If one with the same name
// already exists, its endpoints are updated in place.
func (m *Manager) AddUpstream(name string, endpoints []Endpoint) *Upstream {
	m.mu.Lock()
	defer m.mu.Unlock()

	if u, exists := m.upstreams[name]; exists {
		u.Update(endpoints)
		log.WithField("upstream", name).Info("upstream endpoints updated")
		return u
	}

	u := NewUpstream(name, endpoints)
	m.upstreams[name] = u
	log.WithField("upstream", name).Info("upstream registered")
	return u
}

// GetUpstream returns a registered upstream, or nil.
func (m *Manager) GetUpstream(name string) *Upstream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upstreams[name]
}

// RemoveUpstream unregisters an upstream and unlinks any downstreams
// that were pointing to it. In-flight connections are unaffected.
func (m *Manager) RemoveUpstream(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.upstreams[name]; !exists {
		return
	}
	delete(m.upstreams, name)

	for _, ds := range m.downstreams {
		if u := ds.Upstream(); u != nil && u.Name() == name {
			ds.Unlink()
		}
	}
	log.WithField("upstream", name).Info("upstream removed")
}

// AddDownstream registers a named downstream listener.
func (m *Manager) AddDownstream(name, listenAddr string) *Downstream {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ds, exists := m.downstreams[name]; exists {
		return ds
	}

	ds := NewDownstream(name, listenAddr, nil)
	m.downstreams[name] = ds

	log.WithFields(log.Fields{
		"downstream": name,
		"addr":       listenAddr,
	}).Info("downstream registered")
	return ds
}

// GetDownstream returns a registered downstream, or nil.
func (m *Manager) GetDownstream(name string) *Downstream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.downstreams[name]
}

// RemoveDownstream stops a running downstream, waits for its loop
// to exit, and removes it from the registry.
func (m *Manager) RemoveDownstream(name string) {
	m.mu.Lock()
	h := m.handles[name]
	if h != nil {
		h.cancel()
	}
	delete(m.handles, name)
	delete(m.downstreams, name)
	m.mu.Unlock()

	if h != nil {
		<-h.done
	}
	log.WithField("downstream", name).Info("downstream removed")
}

// Link connects a downstream to an upstream by name and blocks synchronously
// while the downstream runs. It returns when the downstream stops or fails.
func (m *Manager) Link(downstreamName, upstreamName string) error {
	m.mu.Lock()

	ds, ok := m.downstreams[downstreamName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("downstream %q not found", downstreamName)
	}
	u, ok := m.upstreams[upstreamName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("upstream %q not found", upstreamName)
	}

	ds.Link(u)

	// If it's already running under a handle, prevent running it twice
	if _, running := m.handles[downstreamName]; running {
		m.mu.Unlock()
		return fmt.Errorf("downstream %q is already running", downstreamName)
	}

	// Set up tracking handles so RemoveDownstream/Close can still signal cancellation
	ctx, cancel := context.WithCancel(m.ctx)
	h := &dsHandle{cancel: cancel, done: make(chan struct{})}
	m.handles[downstreamName] = h

	// CRITICAL: Unlock before running the blocking downstream execution loop.
	m.mu.Unlock()

	defer func() {
		close(h.done)

		// Clean up the handle registry when execution ends
		m.mu.Lock()
		delete(m.handles, downstreamName)
		m.mu.Unlock()
	}()

	if err := ds.Run(ctx); err != nil {
		log.WithField("downstream", ds.Name()).
			WithError(err).Error("downstream failed")
		return err
	}

	return nil
}

// Unlink disconnects a downstream from its upstream.
func (m *Manager) Unlink(downstreamName string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ds, ok := m.downstreams[downstreamName]
	if !ok {
		return fmt.Errorf("downstream %q not found", downstreamName)
	}
	ds.Unlink()
	return nil
}

// Close cancels all downstream execution contexts and waits for them to exit.
func (m *Manager) Close() {
	m.cancel()

	m.mu.Lock()
	active := make([]*dsHandle, 0, len(m.handles))
	for _, h := range m.handles {
		active = append(active, h)
	}
	m.handles = make(map[string]*dsHandle)
	m.mu.Unlock()

	for _, h := range active {
		<-h.done
	}
	log.Info("proxy manager closed")
}

func HostPort(rawURL string) (string, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0, err
	}

	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}

	var port int

	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
	} else {
		// Infer default ports if missing.
		switch u.Scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		default:
			return "", 0, fmt.Errorf("missing port and unknown scheme %q", u.Scheme)
		}
	}

	return host, port, nil
}
