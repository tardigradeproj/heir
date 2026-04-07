package pki

import (
	"fmt"
	"time"

	"github.com/cloudflare/cfssl/cli/genkey"
	"github.com/cloudflare/cfssl/config"
	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/helpers"
	"github.com/cloudflare/cfssl/initca"
	"github.com/cloudflare/cfssl/signer"
	"github.com/cloudflare/cfssl/signer/local"
)

type CSR struct {
	Name      string
	CN        string
	O         string
	Hostnames []string
}
type Certificate struct {
	Key  []byte
	Cert []byte
}

func GenerateSelfSignedCert() (*Certificate, error) {
	req := &csr.CertificateRequest{
		CN: "kubernetes-ca",
		KeyRequest: &csr.KeyRequest{
			A: "rsa",
			S: 2048,
		},
		Names: []csr.Name{
			{
				C:  "MZZ",
				ST: "MTP",
				L:  "MTP",
				O:  "tardigrade",
				OU: "tardigrade",
			},
		},
		Hosts: []string{},
		CA: &csr.CAConfig{
			Expiry: "87600h", // 10 years
		},
	}
	certPEM, _, keyPEM, err := initca.New(req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate kubernetes CA: %s", err.Error())
	}
	return &Certificate{
		Key:  keyPEM,
		Cert: certPEM,
	}, nil
}

func SignCSR(ca Certificate, request CSR, expire time.Duration) (*Certificate, error) {
	caCertPEM := ca.Key
	caKeyPEM := ca.Cert
	req := &csr.CertificateRequest{
		CN: request.CN,
		Names: []csr.Name{
			{
				O:  request.O,
				OU: request.Name,
			},
		},
		KeyRequest: &csr.KeyRequest{
			A: "rsa",
			S: 2048,
		},
		Hosts: request.Hostnames,
	}

	g := &csr.Generator{Validator: genkey.Validator}
	csrPEM, keyPEM, err := g.ProcessRequest(req)
	if err != nil {
		return nil, err
	}

	policy := &config.Signing{
		Default: &config.SigningProfile{
			Expiry:       expire,
			ExpiryString: expire.String(),
			Usage:        []string{"signing", "key encipherment", "server auth", "client auth"},
		},
	}

	parsedCa, err := helpers.ParseCertificatePEM(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %s", err.Error())
	}
	parsedKey, err := helpers.ParsePrivateKeyPEM(caKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA private key: %s", err.Error())
	}

	s, err := local.NewSigner(parsedKey, parsedCa, signer.DefaultSigAlgo(parsedKey), policy)
	if err != nil {
		return nil, fmt.Errorf("failed to create setup signer: %s", err.Error())
	}

	signReq := signer.SignRequest{
		Request: string(csrPEM),
		Hosts:   request.Hostnames,
	}

	certPEM, err := s.Sign(signReq)
	if err != nil {
		return nil, fmt.Errorf("failed to sign CSR: %s", err.Error())
	}
	return &Certificate{
		Key:  keyPEM,
		Cert: certPEM,
	}, nil
}
