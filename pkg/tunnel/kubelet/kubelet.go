package kubelet

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/tunnel/broker"
)

// errSNIExtracted is a sentinel used to abort the TLS handshake after the
// ClientHello has been read, without sending any response to the client.
var errSNIExtracted = errors.New("sni extracted")

type Server struct {
	listener net.Listener
	broker   *broker.Broker
}

func New(addr string, broker *broker.Broker) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{listener: ln, broker: broker}, nil
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
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {

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
			// Client link broke catastrophically. Abort the tunnel immediately
			// to force the opposing goroutine to unblock and exit.
			_ = tunnel.Close()
			_ = conn.Close()
			return
		}
		// Clean EOF: Client finished sending data normally.
		closeWrite(tunnel)
	}()

	// Direction 2: Tunnel -> Client
	go func() {
		defer wg.Done()
		_, err := io.Copy(conn, tunnel)
		if err != nil {
			// Backend link broke catastrophically. Abort client connection.
			_ = tunnel.Close()
			_ = conn.Close()
			return
		}
		// Clean EOF: Backend finished sending response normally.
		closeWrite(conn)
	}()

	wg.Wait()
	_ = tunnel.Close()
	_ = conn.Close() // Ensure everything is fully cleaned up
}

type recordConn struct {
	net.Conn
	recorded bytes.Buffer
}

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
func (r *recordConn) Read(b []byte) (n int, err error) {
	n, err = r.Conn.Read(b)
	if n > 0 {
		r.recorded.Write(b[:n])
	}
	return n, err
}
