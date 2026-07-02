# Plane Tunnel

## Summary

Heir runs control plane workloads on a Management Cluster. This introduces a connectivity
challenge: components on worker nodes need to reach the control plane, and the control
plane needs to reach back into the worker nodes.

Traffic flows in both directions. For example, a service mesh sidecar
such as Istio's `pilot-agent` connects to `istiod` for certificate rotation and to discover
clusters, endpoints, listeners, and routes. A perfect example of Inbound on the worker side
is the Kubernetes control plane itself, as it must be able to reach the kubelet to stream logs,
execute commands, and forward ports.

In a traditional deployment this is solved by opening inbound traffic between the cloud environment hosting
the Management Cluster and the worker nodes in both directions. Depending on the number of add-ons installed,
this can require a significant number of open ports. It is also insecure, particularly when the Management
Cluster is exposed to the public internet.

Heir runs Kubernetes control plane components as stateless pods on the Management Cluster, scaling them
behind a Network Load Balancer. This introduces a connectivity problem rooted in how konnectivity works:
each worker node runs a konnectivity-agent that must hold a persistent tunnel to every konnectivity-server,
one per konnectivity-server replica. For any API server to reach any node, the full agent × server mesh must be live,
so total open connections grow as N×M, a 100-node cluster behind 3 replicas sustains 300 persistent tunnels,
and every new control plane replica forces every agent to open another, compounding memory and file descriptor overhead at scale.
The other challenges are described below:
- Adding a server replica forces every existing agent to reconnect to it before traffic
  can be load-balanced to the new replica.
- `--server-count` must be kept in sync with the actual replica count, making autoscaling
  the server deployment complex.

This RFC proposes a solution to that challenge.

---

## Proposal

Because worker nodes sit behind NAT, connections must be initiated outbound from the worker side. The proposal
uses TCP multiplexing over a single persistent connection per worker: the agent dials the server, and that
channel is then used bidirectionally — worker-to-control-plane requests and control-plane-to-worker requests
alike flow over the same tunnel. This is implemented with
[outbound](https://github.com/tardigradeproj/outbound), which creates local addresses for remote systems.

**[outbound](https://github.com/tardigradeproj/outbound) does not cover identity.** It provides the multiplexed transport layer but has no notion of who
is on the other end. Authentication is handled separately via mutual TLS: each agent presents a client
certificate when dialing the server, and the server verifies it against the cluster CA before accepting the
connection. The agent's identity is read from the certificate's Common Name field, for example
`CN=system:node:worker2`, so the server knows exactly which node owns the tunnel without any out-of-band
signalling.

### Request Routing

The full request flow is:

```
API server → worker2:10250 → (/etc/hosts) → plane tunnel pod IP:10250 → (SNI peek: worker2) → agent tunnel → kubelet
```

**Prerequisite — API server address type.**
The API server selects the target address for kubelet requests via the `--kubelet-preferred-address-types`
flag. It must be set to:

```
--kubelet-preferred-address-types=Hostname
```

This ensures the API server connects to worker nodes using their registered hostname. The hostname is used
as the TLS ServerName (SNI), which the kubelet's serving certificate always carries as a hostname SAN.
Using `InternalIP` instead would require the kubelet cert to carry an IP SAN — which may be absent and
becomes stale when the node IP changes.

**Step 1 — Master agent builds the routing table.**
Master agent runs as a sidecar container in the API server pod. It periodically resolves the plane tunnel
headless Service DNS to discover all replica Pod IPs, then polls each instance's `/v1/report` endpoint to
learn which worker nodes are connected to it.

From these responses, master agent builds a routing table: worker hostname → plane tunnel Pod IP.

Multiple workers connected to the same replica map to the same Pod IP — this is intentional and valid,
since the plane tunnel uses SNI (not the destination IP) to identify the target worker.

**Step 2 — Master agent writes `/etc/hosts`.**
For each entry in the routing table, master agent writes a line into `/etc/hosts` inside the API server pod:

```
10.0.0.1  worker2
10.0.0.1  worker5
10.0.0.2  worker3
```

When the API server connects to `worker2:10250`, the hostname resolves via `/etc/hosts` to the Pod IP of
the replica that holds worker2's tunnel. TLS is unaffected — the API server used the hostname to initiate
the connection, so the TLS ServerName remains `worker2`.

**Step 3 — Plane tunnel reads SNI and proxies the request.**
The plane tunnel maintains a single listener on port 10250. When a connection arrives, it peeks at the TLS
ClientHello to read the SNI field before any bytes are forwarded. The SNI contains the worker hostname the
API server is targeting. The plane tunnel looks up which agent tunnel belongs to that worker and proxies the
connection through it. The response streams back the same way.

**Rule lifecycle.** Master agent reconciles `/etc/hosts` on every poll cycle: it adds entries for newly
connected nodes, removes entries for nodes that have disconnected, and updates entries for nodes that have
reconnected to a different plane tunnel replica. On restart, master agent clears all managed entries before
rebuilding from scratch.

### Report Endpoint

Each plane tunnel instance exposes an HTTP endpoint that master agent polls to discover which worker
nodes are currently connected to it:

```
GET /v1/report
```

Response body:

```go
type ReportResponse struct {
    Node []ConnectedNode `json:"node"`
}

type ConnectedNode struct {
    Name string `json:"name"` // node name, e.g. "worker2"
}
```

Example:

```json
{
  "node": [
    { "name": "worker2" },
    { "name": "worker5" }
  ]
}
```

An empty `node` array means the instance currently holds no agent tunnels. Master agent must remove any
`/etc/hosts` entries that point to an instance for nodes no longer present in its report.

---

## Properties

**No iptables.**
The design requires no iptables rules in any network namespace. Routing is handled entirely at the
application layer: `/etc/hosts` directs connections to the correct replica, and SNI identifies the
target worker within that replica.

**SNI carries worker identity.**
Because the API server connects to workers by hostname and Go's TLS stack sets the SNI to the dialled
hostname regardless of the resolved IP, the plane tunnel receives `SNI=worker2` even though the TCP
connection was established to a pod IP. The plane tunnel peeks at the ClientHello without consuming it,
reads the SNI field, then forwards the full stream — including the peeked bytes — through the agent tunnel.

**Multiple workers share a Pod IP in `/etc/hosts`.**
Unlike the previous design, no virtual IP allocation is required. Two workers connected to the same
replica both resolve to that replica's real Pod IP. The SNI field is the sole routing key within a
replica, so IP uniqueness per worker is unnecessary.

**No dynamic port binding.**
The plane tunnel never binds a per-worker port. There is one listener on port 10250 and one in-memory
map from worker hostname to agent tunnel. Port exhaustion is not a concern.

**No infrastructure changes required.**
The design requires no changes to the cluster CNI, no dedicated IP CIDRs, no secondary pod IPs, and no
host-level routing configuration. It works on any standard Kubernetes cluster.

**Master agent is limited to `/etc/hosts` management.**
Master agent owns exactly one resource: the set of managed lines in `/etc/hosts` inside the API server
pod. It performs no syscalls, installs no iptables rules, and touches no kernel state.

**Reconnect lag.**
When a worker disconnects from one replica and reconnects to another, `/etc/hosts` continues pointing to
the old replica until master agent's next reconcile cycle completes. Connections to that worker during
this window fail and must be retried by the caller. The lag is bounded by the reconcile period.

**TLS verification works without IP SANs.**
Because the API server connects to worker nodes by hostname and the TLS ServerName is set to that
hostname, the kubelet's serving certificate only needs a hostname SAN — which is always present and
stable. No IP SAN is required. TLS verification is unaffected by node IP changes, certificate rotation
windows, or any mismatch between `node.Status.Addresses` and the certificate.

**API server address type must be configured.**
The only API server change required is setting `--kubelet-preferred-address-types=Hostname`. Beyond
that single flag, the API server is fully unaware of the tunnel infrastructure — it addresses worker
nodes by hostname as it would in a traditional deployment, and the entire mechanism of `/etc/hosts`
resolution, SNI-based routing, and agent tunnel multiplexing operates transparently below the
application layer.
