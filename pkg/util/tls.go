package util

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func loadCertAndPool(certPath, keyPath, caCertPath string) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("failed to load cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return tls.Certificate{}, nil, fmt.Errorf("failed to parse CA cert")
	}

	return cert, pool, nil
}

func SetupTLSConfig(certPath, keyPath, caCertPath string) (*tls.Config, error) {
	cert, pool, err := loadCertAndPool(certPath, keyPath, caCertPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func SetupMTLSServerConfig(certPath, keyPath, caCertPath string) (*tls.Config, error) {
	cert, pool, err := loadCertAndPool(certPath, keyPath, caCertPath)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
