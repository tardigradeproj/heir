package kubelet

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/tunnel/broker"
)

// kubeletUpstreamID is the outbound upstream ID the node agent registers for its local kubelet.
const kubeletUpstreamID uint8 = 1

// errSNIExtracted is a sentinel used to abort the TLS handshake after the
// ClientHello has been read, without sending any response to the client.
var errSNIExtracted = errors.New("sni extracted")

type Server struct {
	listener   net.Listener
	broker     *broker.Broker
	tunnelFQDN string
}

func New(addr string, broker *broker.Broker, tunnelFQDN string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{listener: ln, broker: broker, tunnelFQDN: tunnelFQDN}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	lg := log.WithField("remote", conn.RemoteAddr())
	defer conn.Close()

	recorder := &recordConn{Conn: conn}
	sni, err := extractSNI(recorder)
	if err != nil {
		lg.WithError(err).Warn("failed to extract SNI")
		return
	}
	if sni == "" {
		lg.Warn("dropped connection: no SNI provided")
		return
	}
	lg = lg.WithField("node", sni)

	// If this instance does not hold the tunnel for the target node, forward
	// the raw connection to the peer pod that does.
	if s.broker.Pick(sni) == nil {
		if err := s.forwardToPeer(ctx, sni, recorder.recorded.Bytes(), conn); err != nil {
			lg.WithError(err).Warn("no route to node")
		}
		return
	}

	// Dial the kubelet upstream through the local agent tunnel.
	stream, err := s.broker.Dial(ctx, sni, kubeletUpstreamID)
	if err != nil {
		lg.WithError(err).Warn("failed to dial kubelet upstream through tunnel")
		return
	}
	defer stream.Close()

	// Replay the captured ClientHello so the kubelet sees a complete TLS stream.
	if _, err := stream.Write(recorder.recorded.Bytes()); err != nil {
		lg.WithError(err).Warn("failed to replay ClientHello to kubelet stream")
		return
	}

	splice(conn, stream)
}

// forwardToPeer discovers which peer plane-tunnel pod holds the tunnel for nodeName,
// dials that pod on the same listener port, replays the captured ClientHello, and
// splices the connection through. The peer's own handle resolves the tunnel locally.
func (s *Server) forwardToPeer(ctx context.Context, nodeName string, clientHello []byte, conn net.Conn) error {
	if s.tunnelFQDN == "" {
		return fmt.Errorf("node %q not connected locally and tunnelFQDN is not configured", nodeName)
	}

	peerIP, err := s.findPeer(ctx, nodeName)
	if err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return fmt.Errorf("malformed listener address: %w", err)
	}

	peerConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(peerIP, port))
	if err != nil {
		return fmt.Errorf("dial peer %s: %w", peerIP, err)
	}
	defer peerConn.Close()

	// Send the captured ClientHello so the peer can extract SNI and route locally.
	if _, err := peerConn.Write(clientHello); err != nil {
		return fmt.Errorf("replay ClientHello to peer: %w", err)
	}

	splice(conn, peerConn)
	return nil
}

// findPeer resolves tunnelFQDN to the pod IPs of all plane-tunnel instances,
// queries each pod's /v1/report endpoint, and returns the IP of the pod that
// currently holds a tunnel for nodeName.
func (s *Server) findPeer(ctx context.Context, nodeName string) (string, error) {
	ips, err := net.DefaultResolver.LookupHost(ctx, s.tunnelFQDN)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", s.tunnelFQDN, err)
	}
	for _, ip := range ips {
		nodes, err := queryReport(ctx, ip)
		if err != nil {
			log.WithField("pod", ip).WithError(err).Debug("skipping pod: report unavailable")
			continue
		}
		for _, n := range nodes {
			if n == nodeName {
				return ip, nil
			}
		}
	}
	return "", fmt.Errorf("no peer holds a tunnel for node %q", nodeName)
}

// queryReport calls GET /v1/report on the given pod IP and returns the names of
// the worker nodes currently connected to it. Follows the same contract used by
// masteragent when building the /etc/hosts routing table.
func queryReport(ctx context.Context, podIP string) ([]string, error) {
	url := "http://" + podIP + "/v1/report"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from %s: %s", url, resp.Status)
	}

	var body struct {
		Node []struct {
			Name string `json:"name"`
		} `json:"node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode report from %s: %w", url, err)
	}

	names := make([]string, len(body.Node))
	for i, n := range body.Node {
		names[i] = n.Name
	}
	return names, nil
}

func splice(conn, tunnel net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	closeWrite := func(c net.Conn) {
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			_ = c.Close()
		}
	}

	// Direction 1: Client -> Tunnel
	go func() {
		defer wg.Done()
		_, err := io.Copy(tunnel, conn)
		if err != nil {
			_ = tunnel.Close()
			_ = conn.Close()
			return
		}
		closeWrite(tunnel)
	}()

	// Direction 2: Tunnel -> Client
	go func() {
		defer wg.Done()
		_, err := io.Copy(conn, tunnel)
		if err != nil {
			_ = tunnel.Close()
			_ = conn.Close()
			return
		}
		closeWrite(conn)
	}()

	wg.Wait()
	_ = tunnel.Close()
	_ = conn.Close()
}

// recordConn wraps net.Conn and captures every byte read into a buffer so the
// ClientHello can be replayed to the backend after SNI extraction.
type recordConn struct {
	net.Conn
	recorded bytes.Buffer
}

// extractSNI performs an aborted TLS handshake that reads only the ClientHello,
// capturing the ServerName without sending any bytes back to the client.
func extractSNI(conn net.Conn) (string, error) {
	var sni string
	cfg := &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sni = hello.ServerName
			return nil, errSNIExtracted
		},
	}
	if err := tls.Server(conn, cfg).Handshake(); !errors.Is(err, errSNIExtracted) {
		return "", err
	}
	return sni, nil
}
