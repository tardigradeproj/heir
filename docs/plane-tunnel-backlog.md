# Plane Tunnel — Implementation Backlog

Work is split into phases ordered by dependency. Each item can be a PR or a logical commit unit.

---

## Phase 1 — Foundation

**1.1 Add `outbound` dependency**
- `go get github.com/tardigradeproj/outbound` and tidy
- Confirm the API surface needed: dialing a server, obtaining a local `net.Addr` for the remote end

**1.2 Config structs (`pkg/tunnel/`)**
- `AgentConfig`: server address, client cert/key paths, CA path, reconnect backoff
- `ServerConfig`: listen address, CA path, server cert/key paths, lease namespace, own Pod IP, peer forward port
- Mirror the pattern used by `pkg/masteragent/type.go`

**1.3 CLI wiring (`cmd/tunnel/app.go` + `cmd/tunnel.go`)**
- Two sub-commands: `heir tunnel agent` and `heir tunnel server`
- Flags + env bindings (pattern from `cmd/masteragent/app.go`)
- Wire into the main cobra tree in `cmd/main.go`

---

## Phase 2 — mTLS Layer

**2.1 Agent TLS config**
- Load client cert/key pair and CA cert
- Build `tls.Config` with `tls.RequireAndVerifyClientCert` on the dialer side
- `cloudflare/cfssl` is already in go.mod; can also use stdlib `crypto/tls`

**2.2 Server TLS config**
- Load server cert/key and CA
- Build `tls.Config` with `ClientAuth: tls.RequireAndVerifyClientCert`
- Helper to extract node name from `CN=system:node:<name>` in verified peer certificate

---

## Phase 3 — Agent

**3.1 Dial and tunnel establishment (`pkg/tunnel/agent.go`)**
- Dial the server address using mTLS config
- Hand the connection to `outbound` to establish the bidirectional multiplexed tunnel

**3.2 Reconnect loop**
- On disconnect, retry with exponential backoff
- Log node name and server address on each attempt

---

## Phase 4 — Server: Connection Handling

**4.1 TLS listener (`pkg/tunnel/server.go`)**
- `tls.Listen` on configured address
- Accept loop: spawn a goroutine per connection

**4.2 Per-connection setup**
- Extract node name from peer TLS certificate CN
- Register the connection with `outbound` to get a local address for that agent's tunnel
- Track active connections in a `map[nodeName]outboundConn` (protected by `sync.RWMutex`)
- On connection close: deregister from map

---

## Phase 5 — Lease Management

**5.1 Lease writer (`pkg/tunnel/lease.go`)**
- On agent connect: create or update a `coordination.k8s.io/v1` Lease in the configured namespace
  - `metadata.name` = node name
  - `spec.holderIdentity` = own Pod IP
  - `spec.leaseDurationSeconds` = 15
- Heartbeat goroutine: renew (`renewTime`) every ~5 s while the agent is connected
- On agent disconnect: delete the lease (or let it expire — deletion is cleaner)

**5.2 RBAC manifest (`config/tunnel/rbac.yaml`)**
- `ServiceAccount`, `ClusterRole`, `ClusterRoleBinding` (or `Role`/`RoleBinding` scoped to lease namespace)
- Verbs: `get list watch create update patch delete` on `coordination.k8s.io/leases`

---

## Phase 6 — Routing Index

**6.1 `AgentLeaseIndex` type (`pkg/tunnel/index.go`)**
- `map[string]string` (nodeName → server Pod IP) behind `sync.RWMutex`
- Methods: `Add`, `Update`, `Delete`, `Lookup`

**6.2 Informer setup**
- Use `k8s.io/client-go` `SharedInformer` watching leases in the tunnel namespace
- Event handlers call `index.Add/Update/Delete`
- Start informer cache sync before the server begins accepting connections

---

## Phase 7 — Inbound Proxy (API Server → Worker)

**7.1 Local forward handler**
- When a request arrives for a node whose tunnel is held locally, proxy it through the `outbound` local address for that node

**7.2 Inter-server forwarding**
- When the routing index shows a different Pod IP holds the target node's tunnel, forward the raw request to `http://<peerIP>:<forwardPort>` over an internal HTTP channel
- The receiving peer then handles it as a local request (step 7.1)
- Return errors clearly if no lease exists for the requested node

---

## Phase 8 — Tests

**8.1 Unit: `AgentLeaseIndex`**
- Concurrent add/lookup/delete, ErrNotFound on missing entry

**8.2 Unit: node name extraction**
- Parse `CN=system:node:<name>` correctly, reject malformed CNs

**8.3 Integration: agent ↔ server round-trip**
- Spin up a server and agent in-process with self-signed certs
- Verify a request proxied through reaches the agent side

---

## Suggested Execution Order

```
1.1 → 1.2 → 2.1/2.2 (parallel) → 3.1 → 3.2
                                          ↓
                              4.1 → 4.2 → 5.1 → 5.2
                                                   ↓
                                          6.1 → 6.2
                                                   ↓
                                          7.1 → 7.2
                                                   ↓
                                           1.3 + 8.x
```

Wire the CLI last (1.3) so each internal package can be exercised through tests before it's exposed as a command.
