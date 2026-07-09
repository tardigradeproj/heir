package egress_selector

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/broker"
)

// kubeletUpstreamID is the outbound upstream ID the node agent registers for its local kubelet.
const kubeletUpstreamID uint8 = 1

type brokerDialer interface {
	Dial(ctx context.Context, nodeName string, upstreamID uint8) (net.Conn, error)
}

type Server struct {
	srv            *http.Server
	broker         brokerDialer
	serverCertPath string
	serverKeyPath  string
	caCertPath     string
}

func New(addr, serverCertPath, serverKeyPath, caCertPath string, broker *broker.Broker) *Server {
	s := &Server{
		broker:         broker,
		serverCertPath: serverCertPath,
		serverKeyPath:  serverKeyPath,
		caCertPath:     caCertPath,
	}
	s.srv = &http.Server{
		Addr:    addr,
		Handler: http.HandlerFunc(s.handle),
	}
	return s
}

func (s *Server) Serve(ctx context.Context) error {
	serverCert, err := tls.LoadX509KeyPair(s.serverCertPath, s.serverKeyPath)
	if err != nil {
		return fmt.Errorf("failed to load server cert/key: %w", err)
	}
	caPEM, err := os.ReadFile(s.caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA cert: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("failed to append CA cert")
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12,
	}
	ln, err := tls.Listen("tcp", s.srv.Addr, tlsConfig)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = s.srv.Shutdown(context.Background())
	}()
	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	log := logrus.WithFields(logrus.Fields{"method": r.Method, "host": r.Host, "userAgent": r.UserAgent()})
	log.Debug("received request")
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		log.WithField("commonName", r.TLS.PeerCertificates[0].Subject.CommonName).Debug("TLS client identity")
	}
	if r.Method != http.MethodConnect {
		http.Error(w, "this proxy only supports CONNECT passthrough", http.StatusMethodNotAllowed)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nodeName, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		nodeName = r.Host
	}

	lg := log.WithField("node", nodeName)

	stream, err := s.broker.Dial(r.Context(), nodeName, kubeletUpstreamID)
	if err != nil {
		lg.WithError(err).Warn("failed to dial kubelet upstream")
		fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		conn.Close()
		return
	}
	defer stream.Close()

	if _, err := fmt.Fprintf(bufrw, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		lg.WithError(err).Warn("failed to write 200 response")
		conn.Close()
		return
	}
	if err := bufrw.Flush(); err != nil {
		lg.WithError(err).Warn("failed to flush 200 response")
		conn.Close()
		return
	}

	splice(&hijackedConn{Conn: conn, r: bufrw.Reader}, stream)
}

// hijackedConn wraps a hijacked net.Conn so that reads first drain the buffered
// reader left by the HTTP server before falling through to the raw connection.
type hijackedConn struct {
	net.Conn
	r *bufio.Reader
}

func (h *hijackedConn) Read(b []byte) (int, error) {
	return h.r.Read(b)
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
