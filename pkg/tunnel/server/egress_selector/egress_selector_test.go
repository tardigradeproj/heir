package egress_selector

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockDialer satisfies brokerDialer for tests.
type mockDialer struct {
	conn net.Conn
	err  error
}

func (m *mockDialer) Dial(_ context.Context, _ string, _ uint8) (net.Conn, error) {
	return m.conn, m.err
}

type result struct {
	statusLine string
	toNode     string
	toClient   string
}

func assertion(t *testing.T, got, want result) {
	t.Helper()
	assert.Equal(t, want.statusLine, got.statusLine)
	assert.Equal(t, want.toNode, got.toNode)
	assert.Equal(t, want.toClient, got.toClient)
}

func TestHandle(t *testing.T) {
	// Pre-create the pipe for the proxy case so both ends can be referenced
	// inside the cases slice.
	handlerSide, nodeSide := net.Pipe()

	cases := []struct {
		name     string
		dialer   brokerDialer
		nodeSide net.Conn // non-nil only for the successful proxy case
		rawReq   string
		want     result
	}{
		{
			name:   "rejects non-CONNECT method with 405",
			dialer: &mockDialer{},
			rawReq: "GET / HTTP/1.1\r\nHost: test\r\n\r\n",
			want:   result{statusLine: "HTTP/1.1 405 Method Not Allowed"},
		},
		{
			name:   "returns 502 when broker dial fails",
			dialer: &mockDialer{err: errors.New("no tunnel for node")},
			rawReq: "CONNECT worker2:10250 HTTP/1.1\r\nHost: worker2:10250\r\n\r\n",
			want:   result{statusLine: "HTTP/1.1 502 Bad Gateway"},
		},
		{
			name:     "establishes tunnel and proxies bidirectionally",
			dialer:   &mockDialer{conn: handlerSide},
			nodeSide: nodeSide,
			rawReq:   "CONNECT worker2:10250 HTTP/1.1\r\nHost: worker2:10250\r\n\r\n",
			want: result{
				statusLine: "HTTP/1.1 200 Connection Established",
				toNode:     "hello from client",
				toClient:   "hello from node",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{broker: tc.dialer}
			srv := httptest.NewServer(http.HandlerFunc(s.handle))
			defer srv.Close()

			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			if _, err = conn.Write([]byte(tc.rawReq)); err != nil {
				t.Fatal(err)
			}

			br := bufio.NewReader(conn)
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			got := result{statusLine: strings.TrimRight(line, "\r\n")}

			if tc.nodeSide != nil {
				defer tc.nodeSide.Close()

				// Drain remaining response headers until the blank line that ends
				// the 200 response, putting br at the start of the tunnel stream.
				for {
					line, _ = br.ReadString('\n')
					if strings.TrimRight(line, "\r\n") == "" {
						break
					}
				}

				const (
					clientMsg = "hello from client"
					nodeMsg   = "hello from node"
				)
				var (
					wg       sync.WaitGroup
					toNode   string
					toClient string
				)
				wg.Add(2)

				// Client side: write to the tunnel, then read what the node sent back.
				go func() {
					defer wg.Done()
					_, _ = conn.Write([]byte(clientMsg))
					buf := make([]byte, len(nodeMsg))
					_, _ = io.ReadFull(br, buf)
					toClient = string(buf)
				}()

				// Node side: read what the client sent, then reply.
				go func() {
					defer wg.Done()
					buf := make([]byte, len(clientMsg))
					_, _ = io.ReadFull(tc.nodeSide, buf)
					toNode = string(buf)
					_, _ = tc.nodeSide.Write([]byte(nodeMsg))
				}()

				wg.Wait()
				got.toNode = toNode
				got.toClient = toClient
			}

			assertion(t, got, tc.want)
		})
	}
}

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
