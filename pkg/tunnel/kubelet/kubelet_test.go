package kubelet

import (
	"crypto/tls"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplice(t *testing.T) {
	assertion := func(t *testing.T, got, want []byte) {
		t.Helper()
		assert.Equal(t, want, got)
	}

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "forwards data from conn to tunnel",
			run: func(t *testing.T) {
				connA, connB := net.Pipe()
				tunnelA, tunnelB := net.Pipe()
				go splice(connB, tunnelB)
				go func() {
					connA.Write([]byte("hello from client"))
					connA.Close()
				}()
				got := make([]byte, len("hello from client"))
				tunnelA.Read(got)
				assertion(t, got, []byte("hello from client"))
			},
		},
		{
			name: "tunnel continues streaming after conn finishes",
			run: func(t *testing.T) {
				connClient, connServer := tcpPipe(t)
				tunnelClient, tunnelServer := tcpPipe(t)
				defer connClient.Close()
				defer tunnelClient.Close()

				spliceDone := make(chan struct{})
				go func() {
					defer close(spliceDone)
					splice(connServer, tunnelServer)
				}()

				_, _ = connClient.Write([]byte("from client"))
				_ = connClient.CloseWrite()

				fromClient := make([]byte, len("from client"))
				_, _ = io.ReadFull(tunnelClient, fromClient)
				_, _ = tunnelClient.Write([]byte("from tunnel"))
				_ = tunnelClient.CloseWrite()

				fromTunnel := make([]byte, len("from tunnel"))
				_, _ = io.ReadFull(connClient, fromTunnel)
				<-spliceDone

				assertion(t, fromClient, []byte("from client"))
				assertion(t, fromTunnel, []byte("from tunnel"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

// tcpPipe returns a connected pair of TCP connections. Unlike net.Pipe, *net.TCPConn
// implements CloseWrite, which lets splice half-close one direction independently.
func tcpPipe(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan *net.TCPConn, 1)
	go func() {
		c, _ := ln.Accept()
		ch <- c.(*net.TCPConn)
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	client = c.(*net.TCPConn)
	server = <-ch
	ln.Close()
	return
}

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
