package proxy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sync"

	log "github.com/sirupsen/logrus"
	"inet.af/tcpproxy"
)

// APIServerProxy forwards local 127.0.0.1:6443 traffic to remote apiservers.
// It fails over to the next server when a connection attempt fails.
type APIServerProxy struct {
	mu      sync.RWMutex
	servers []string // remote apiserver addresses
	proxy   *tcpproxy.Proxy
}

func New(initialServerAddrs []string) (*APIServerProxy, error) {
	parsedAddresses, err := parseApiServerExternalAddresses(initialServerAddrs)
	if err != nil {
		return nil, err
	}
	return &APIServerProxy{servers: parsedAddresses}, nil
}

// Run starts listening on 127.0.0.1:6443 and forwards connections to the
// configured apiserver addresses. It blocks until ctx is cancelled.
func (p *APIServerProxy) Run(ctx context.Context) error {
	if len(p.servers) == 0 {
		return fmt.Errorf("no proxy servers defined")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:6443")
	if err != nil {
		return err
	}

	p.proxy = &tcpproxy.Proxy{
		ListenFunc: func(_, _ string) (net.Listener, error) {
			return listener, nil
		},
	}

	p.proxy.AddRoute("apiserver", &tcpproxy.DialProxy{
		Addr: "apiserver",
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return p.dial(ctx, network)
		},
		OnDialError: func(src net.Conn, err error) {
			log.Errorf("incoming conn %s, error dialing apiserver: %v", src.RemoteAddr(), err)
			src.Close()
		},
	})

	if err := p.proxy.Start(); err != nil {
		listener.Close()
		return fmt.Errorf("failed to start apiserver proxy: %w", err)
	}

	<-ctx.Done()
	p.proxy.Close()
	return nil
}

// UpdateServers replaces the list of remote apiserver addresses.
func (p *APIServerProxy) UpdateServers(addrs []string) error {
	parsedAddresses, err := parseApiServerExternalAddresses(addrs)
	if err != nil {
		return err
	}
	log.WithField("parsed.addrs", parsedAddresses).
		WithField("not-parsed.addrs", addrs).
		Info("updating servers")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.servers = parsedAddresses
	return nil
}

// dial tries each server in order, returning the first successful connection.
func (p *APIServerProxy) dial(ctx context.Context, network string) (net.Conn, error) {
	p.mu.RLock()
	servers := make([]string, len(p.servers))
	copy(servers, p.servers)
	p.mu.RUnlock()

	var lastErr error
	for _, addr := range servers {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no apiserver addresses available")
}

// parseApiServerExternalAddresses parses a slice of API server URLs and returns a
// deduplicated slice of host:port strings.
func parseApiServerExternalAddresses(rawURLs []string) ([]string, error) {
	seen := make(map[string]struct{})
	var addresses []string
	for _, raw := range rawURLs {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse address %q: %w", raw, err)
		}
		hostPort := net.JoinHostPort(u.Hostname(), u.Port())
		if u.Port() == "" {
			hostPort = u.Hostname()
		}
		if _, ok := seen[hostPort]; ok {
			continue
		}
		seen[hostPort] = struct{}{}
		addresses = append(addresses, hostPort)
	}
	return addresses, nil
}
