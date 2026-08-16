package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	obs "github.com/tardigradeproj/heir/pkg/observability"
	"github.com/tardigradeproj/heir/pkg/tunnel/server/broker"
	"gvisor.dev/gvisor/pkg/cleanup"
)

type TunnelServer struct {
	serverCertPath string
	serverKeyPath  string
	caCertPath     string
	listenAddr     string
	broker         *broker.Broker
}

func NewTunnelServer(
	serverCertPath, serverKeyPath, caCertPath, listenAddr string,
	broker *broker.Broker) *TunnelServer {
	return &TunnelServer{
		serverCertPath: serverCertPath,
		serverKeyPath:  serverKeyPath,
		caCertPath:     caCertPath,
		listenAddr:     listenAddr,
		broker:         broker,
	}
}

func (b *TunnelServer) Serve(ctx context.Context) error {
	log := logrus.WithFields(logrus.Fields{
		obs.Component: "tunnel",
	})
	serverCert, err := tls.LoadX509KeyPair(b.serverCertPath, b.serverKeyPath)
	if err != nil {
		log.WithError(err).Error("Failed to load server certificate and key")
		return fmt.Errorf("failed to load server cert/key: %w", err)
	}
	caPEM, err := os.ReadFile(b.caCertPath)
	if err != nil {
		log.WithError(err).Error("failed to read CA certificate")
		return fmt.Errorf("failed to read ca cert: %w", err)
	}
	// Load the CA certificate used to sign your Client Certificates
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		log.Error("failed to append ca cert")
		return fmt.Errorf("failed to append ca cert")
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12, // Enforce modern TLS security
	}
	// Start the mTLS Listener
	listener, err := tls.Listen("tcp", b.listenAddr, tlsConfig)
	if err != nil {
		log.WithError(err).Error("failed to start tls listener")
		return fmt.Errorf("failed to start listener: %w", err)
	}
	defer listener.Close()
	log.WithField(obs.Addr, b.listenAddr).Info("Listening for connections")
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.WithError(err).Error("Failed to accept connection")
			continue
		}
		go b.handle(ctx, log, conn)
	}
}

func (b *TunnelServer) handle(ctx context.Context, log *logrus.Entry, conn net.Conn) {
	cleaner := cleanup.Make(func() {})

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		conn.Close()
		return
	}
	defer cleaner.Clean()
	cleaner.Add(func() {
		if err := tlsConn.Close(); err != nil {
			log.WithError(err).Error("failed to close connection")
		}
	})
	_ = tlsConn.SetDeadline(time.Now().Add(10 * time.Second)) // bound the handshake only
	if err := tlsConn.Handshake(); err != nil {
		log.WithError(err).Warn("failed to handshake connection")
		return
	}
	_ = tlsConn.SetDeadline(time.Time{}) // long-lived: clear it

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		log.Warn("no client certificate")
		return
	}
	clientCN := state.PeerCertificates[0].Subject.CommonName
	log.WithField("client.cert.CN", clientCN)
	nodeName, err := extractNodeName(clientCN)
	if err != nil {
		log.WithError(err).Warn("failed to extract node name")
		return
	}
	log.WithField(obs.NodeName, nodeName).Debug("extracted node name")
	tunnelId, err := b.broker.Register(ctx, nodeName, tlsConn)
	if err != nil {
		log.WithError(err).Warn("failed to register connection tunnel")
	}
	log.WithFields(logrus.Fields{
		"tunnel.id":  tunnelId,
		obs.NodeName: nodeName,
	}).Info("connection tunnel successfully registered")
	cleaner.Release()
}

func extractNodeName(cn string) (string, error) {
	const prefix = "system:node:"

	if !strings.HasPrefix(cn, prefix) {
		return "", errors.New("common name on client certificate does not match expected system:node:<node-name> format")
	}

	nodeName := strings.TrimPrefix(cn, prefix)
	if nodeName == "" {
		return "", errors.New("node name is empty")
	}

	return nodeName, nil
}
