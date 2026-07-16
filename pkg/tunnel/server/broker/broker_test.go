package broker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/outbound"
)

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registry  func() *outbound.Registry
		name      string
		assertion func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error)
	}{
		{
			name: "returns non-empty id and no error",
			registry: func() *outbound.Registry {
				return outbound.NewRegistry()
			},
			assertion: func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error) {
				defer cancel()
				require.NoError(t, err)
				require.NotEmpty(t, id)
			},
		},
		{
			name: "tunnel is reachable via Pick after registration",
			registry: func() *outbound.Registry {
				return outbound.NewRegistry()
			},
			assertion: func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error) {
				defer cancel()
				require.NoError(t, err)
				tun := b.Pick(nodeName)
				require.NotNil(t, tun)
				require.Equal(t, id, tun.id)
			},
		},
		{
			name: "second registration for same node gets distinct id and both tunnels are tracked",
			registry: func() *outbound.Registry {
				return outbound.NewRegistry()
			},
			assertion: func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error) {
				defer cancel()
				require.NoError(t, err)

				remote2, local2 := net.Pipe()
				t.Cleanup(func() { _ = remote2.Close() })

				id2, err2 := b.Register(ctx, nodeName, local2)
				require.NoError(t, err2)
				require.NotEqual(t, id, id2)

				b.mu.RLock()
				count := len(b.nodes[nodeName].conns)
				b.mu.RUnlock()
				require.Equal(t, 2, count)
			},
		},
		{
			name: "unregisters tunnel on context cancellation",
			registry: func() *outbound.Registry {
				return outbound.NewRegistry()
			},
			assertion: func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error) {
				require.NoError(t, err)
				require.NotNil(t, b.Pick(nodeName))

				cancel()
				require.Eventually(t, func() bool {
					return b.Pick(nodeName) == nil
				}, 2*time.Second, 10*time.Millisecond)
			},
		},
		{
			name: "unregisters tunnel when remote end closes",
			registry: func() *outbound.Registry {
				return outbound.NewRegistry()
			},
			assertion: func(t *testing.T, b *Broker, ctx context.Context, cancel context.CancelFunc, nodeName string, remoteConn net.Conn, id string, err error) {
				defer cancel()
				require.NoError(t, err)
				require.NotNil(t, b.Pick(nodeName))

				_ = remoteConn.Close()
				require.Eventually(t, func() bool {
					return b.Pick(nodeName) == nil
				}, 2*time.Second, 10*time.Millisecond)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := New(tc.registry(), 30*time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			remoteConn, localConn := net.Pipe()
			t.Cleanup(func() {
				cancel()
				_ = remoteConn.Close()
				_ = localConn.Close()
			})

			id, err := b.Register(ctx, "test-node", localConn)
			tc.assertion(t, b, ctx, cancel, "test-node", remoteConn, id, err)
		})
	}
}

func TestUnregister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		assertion func(t *testing.T, b *Broker)
	}{
		{
			name: "no-op when node does not exist",
			assertion: func(t *testing.T, b *Broker) {
				b.Unregister("ghost-node", "any-id")

				b.mu.RLock()
				count := len(b.nodes)
				b.mu.RUnlock()
				require.Equal(t, 0, count)
			},
		},
		{
			name: "no-op when id is not found in existing node",
			assertion: func(t *testing.T, b *Broker) {
				seedTunnel(t, b, "node-1", "id-A")
				b.Unregister("node-1", "id-Z")

				require.NotNil(t, b.Pick("node-1"))
			},
		},
		{
			name: "removes the matching tunnel by id",
			assertion: func(t *testing.T, b *Broker) {
				seedTunnel(t, b, "node-1", "id-A")
				b.Unregister("node-1", "id-A")

				require.Nil(t, b.Pick("node-1"))
			},
		},
		{
			name: "deletes node entry when its last tunnel is removed",
			assertion: func(t *testing.T, b *Broker) {
				seedTunnel(t, b, "node-1", "id-A")
				b.Unregister("node-1", "id-A")

				b.mu.RLock()
				_, exists := b.nodes["node-1"]
				b.mu.RUnlock()
				require.False(t, exists)
			},
		},
		{
			name: "leaves remaining tunnels intact when one of many is removed",
			assertion: func(t *testing.T, b *Broker) {
				seedTunnel(t, b, "node-1", "id-A")
				_, tunB := seedTunnel(t, b, "node-1", "id-B")
				b.Unregister("node-1", "id-A")

				picked := b.Pick("node-1")
				require.NotNil(t, picked)
				require.Same(t, tunB, picked.tunnel)
			},
		},
		{
			name: "idempotent: second unregister on the same id is a no-op",
			assertion: func(t *testing.T, b *Broker) {
				seedTunnel(t, b, "node-1", "id-A")
				b.Unregister("node-1", "id-A")
				b.Unregister("node-1", "id-A")

				b.mu.RLock()
				_, exists := b.nodes["node-1"]
				b.mu.RUnlock()
				require.False(t, exists)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := New(outbound.NewRegistry(), 30*time.Second)
			tc.assertion(t, b)
		})
	}
}

func TestPick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		assertion func(t *testing.T, b *Broker)
	}{
		{
			name: "returns nil for unknown node",
			assertion: func(t *testing.T, b *Broker) {
				require.Nil(t, b.Pick("ghost"))
			},
		},
		{
			name: "returns the single registered tunnel",
			assertion: func(t *testing.T, b *Broker) {
				_, tun := seedTunnel(t, b, "node-1", "id-A")
				picked := b.Pick("node-1")
				require.NotNil(t, picked)
				require.Same(t, tun, picked.tunnel)
			},
		},
		{
			name: "round-robins over two tunnels across successive calls",
			assertion: func(t *testing.T, b *Broker) {
				_, tunA := seedTunnel(t, b, "node-1", "id-A") // index 0
				_, tunB := seedTunnel(t, b, "node-1", "id-B") // index 1
				// rr=0 on a fresh broker; AddUint64 yields 1,2,3,4 → indices 1%2,0,1,0
				require.Same(t, tunB, b.Pick("node-1").tunnel)
				require.Same(t, tunA, b.Pick("node-1").tunnel)
				require.Same(t, tunB, b.Pick("node-1").tunnel)
				require.Same(t, tunA, b.Pick("node-1").tunnel)
			},
		},
		{
			name: "skips closed tunnel and returns the open one",
			assertion: func(t *testing.T, b *Broker) {
				_, tunA := seedTunnel(t, b, "node-1", "id-A") // index 0
				_, tunB := seedTunnel(t, b, "node-1", "id-B") // index 1
				require.NoError(t, tunA.Close())

				for range 4 {
					picked := b.Pick("node-1")
					require.NotNil(t, picked)
					require.Same(t, tunB, picked.tunnel)
				}
			},
		},
		{
			name: "returns nil when all tunnels are closed",
			assertion: func(t *testing.T, b *Broker) {
				_, tun := seedTunnel(t, b, "node-1", "id-A")
				require.NoError(t, tun.Close())

				require.Nil(t, b.Pick("node-1"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := New(outbound.NewRegistry(), 30*time.Second)
			tc.assertion(t, b)
		})
	}
}

// seedTunnel inserts a Tunnel with the given id directly into b.nodes, bypassing Register's
// lifecycle goroutine so callers get fully deterministic broker state.
func seedTunnel(t *testing.T, b *Broker, nodeName, id string) (*Tunnel, *outbound.Tunnel) {
	t.Helper()
	remote, local := net.Pipe()
	session, err := outbound.Server(local, outbound.DefaultConfig())
	require.NoError(t, err)
	tun := outbound.NewTunnel(session, outbound.NewRegistry())

	entry := &Tunnel{id: id, tunnel: tun, cnn: local}
	b.mu.Lock()
	set := b.nodes[nodeName]
	if set == nil {
		set = &tunnelSet{}
		b.nodes[nodeName] = set
	}
	set.conns = append(set.conns, entry)
	b.mu.Unlock()

	t.Cleanup(func() {
		_ = tun.Close()
		_ = remote.Close()
	})
	return entry, tun
}
