package agent

import (
	"context"
	"net"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstablishNewConnection(t *testing.T) {
	pki := newTestPKI(t)
	wantIdentity := &PlaneTunnelIdentity{Id: "abc-123", NumberOfInstances: 3}

	type callResult struct {
		err        error
		trackerLen int
		target     int
	}

	assertion := func(t *testing.T, got callResult, wantErr bool, wantTrackerLen, wantTarget int) {
		t.Helper()
		if wantErr {
			require.Error(t, got.err)
		} else {
			require.NoError(t, got.err)
		}
		assert.Equal(t, wantTrackerLen, got.trackerLen, "tracker length")
		if !wantErr {
			assert.Equal(t, wantTarget, got.target, "target replica count")
		}
	}

	cases := []struct {
		name           string
		setupServer    func(t *testing.T) string
		prepTracker    func(a *Agent)
		ctxTimeout     time.Duration
		wantErr        bool
		wantTrackerLen int
		wantTarget     int
	}{
		{
			name: "new connection is registered in tracker and target is updated",
			setupServer: func(t *testing.T) string {
				return newMTLSServer(t, pki, outboundHandler(wantIdentity))
			},
			ctxTimeout:     5 * time.Second,
			wantErr:        false,
			wantTrackerLen: 1,
			wantTarget:     3,
		},
		{
			name: "connection failure returns error and tracker remains empty",
			setupServer: func(t *testing.T) string {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				addr := ln.Addr().String()
				require.NoError(t, ln.Close())
				return addr
			},
			// Short timeout cuts the retry loop after the first failed attempt
			// instead of waiting 3 s × 3 retry delays.
			ctxTimeout:     2 * time.Second,
			wantErr:        true,
			wantTrackerLen: 0,
		},
		{
			name: "duplicate identity is rejected and existing tracker entry is preserved",
			setupServer: func(t *testing.T) string {
				return newMTLSServer(t, pki, outboundHandler(wantIdentity))
			},
			prepTracker: func(a *Agent) {
				a.conn.mu.Lock()
				a.conn.tracker[wantIdentity.Id] = managedConn{}
				a.conn.mu.Unlock()
			},
			// Short timeout: context expires during the 3 s retry delay so only
			// one attempt is made, keeping the test fast.
			ctxTimeout:     500 * time.Millisecond,
			wantErr:        true,
			wantTrackerLen: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := tc.setupServer(t)
			a := newTestAgent(t, pki, addr)
			if tc.prepTracker != nil {
				tc.prepTracker(a)
			}

			ctx, cancel := context.WithTimeout(context.Background(), tc.ctxTimeout)
			defer cancel()

			err := a.establishNewConnection(ctx, log.WithField("test", t.Name()))

			a.conn.mu.RLock()
			got := callResult{
				err:        err,
				trackerLen: len(a.conn.tracker),
				target:     a.conn.target,
			}
			a.conn.mu.RUnlock()

			assertion(t, got, tc.wantErr, tc.wantTrackerLen, tc.wantTarget)
		})
	}
}

// TestEstablishNewConnectionCleanup verifies that the serve goroutine removes
// the tracker entry and pushes to the disconnected channel when the tunnel
// closes.
func TestEstablishNewConnectionCleanup(t *testing.T) {
	pki := newTestPKI(t)
	wantIdentity := &PlaneTunnelIdentity{Id: "abc-123", NumberOfInstances: 3}

	assertion := func(t *testing.T, disconnectedID string, trackerStillPresent bool) {
		t.Helper()
		assert.Equal(t, wantIdentity.Id, disconnectedID, "disconnected channel must carry the instance ID")
		assert.False(t, trackerStillPresent, "tracker entry must be removed after tunnel closes")
	}

	addr := newMTLSServer(t, pki, outboundHandler(wantIdentity))
	a := newTestAgent(t, pki, addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, a.establishNewConnection(ctx, log.WithField("test", t.Name())))

	a.conn.mu.RLock()
	entry, ok := a.conn.tracker[wantIdentity.Id]
	a.conn.mu.RUnlock()
	require.True(t, ok, "entry must be in tracker before triggering disconnect")

	_ = entry.tunnel.Close()

	var disconnectedID string
	select {
	case disconnectedID = <-a.conn.disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("serve goroutine did not signal disconnect in time")
	}

	a.conn.mu.RLock()
	_, stillPresent := a.conn.tracker[wantIdentity.Id]
	a.conn.mu.RUnlock()

	assertion(t, disconnectedID, stillPresent)
}
