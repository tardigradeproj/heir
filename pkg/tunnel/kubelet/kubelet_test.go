package kubelet

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSNI(t *testing.T) {
	cases := []struct {
		name     string
		clientFn func(net.Conn)
		wantSNI  string
		wantErr  bool
	}{
		{
			name: "extracts SNI from ClientHello",
			clientFn: func(c net.Conn) {
				cfg := &tls.Config{
					ServerName:         "node-1.example.com",
					InsecureSkipVerify: true,
				}
				_ = tls.Client(c, cfg).Handshake()
			},
			wantSNI: "node-1.example.com",
		},
		{
			name: "returns empty string when no SNI in ClientHello",
			clientFn: func(c net.Conn) {
				cfg := &tls.Config{
					InsecureSkipVerify: true,
				}
				_ = tls.Client(c, cfg).Handshake()
			},
			wantSNI: "",
		},
		{
			name: "returns error on non-TLS data",
			clientFn: func(c net.Conn) {
				_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
			},
			wantErr: true,
		},
		{
			name: "returns error on immediate connection close",
			clientFn: func(c net.Conn) {
				c.Close()
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()

			go func() {
				defer client.Close()
				tc.clientFn(client)
			}()

			gotSNI, err := extractSNI(server)
			assertion(t, gotSNI, err, tc.wantSNI, tc.wantErr)
		})
	}
}

func assertion(t *testing.T, gotSNI string, err error, wantSNI string, wantErr bool) {
	t.Helper()
	if wantErr {
		assert.Error(t, err)
		return
	}
	assert.NoError(t, err)
	assert.Equal(t, wantSNI, gotSNI)
}
