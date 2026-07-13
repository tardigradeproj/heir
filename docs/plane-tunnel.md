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
This design implicitly assumes that the API server is a fixed, singleton process that does not scale out
dynamically. In Heir's model the API server runs as a replicated, stateless deployment and may scale
horizontally at any time, an assumption konnectivity was never built to accommodate.
The other challenges are described below:

* Adding a server replica forces every existing agent to reconnect to it before traffic
  can be load-balanced to the new replica.
* `--server-count` must be kept in sync with the actual replica count, making autoscaling
  the server deployment complex.

This RFC proposes a solution to that challenge.

---

## Proposal

Because worker nodes sit behind NAT, connections must be initiated outbound from the worker side. The proposal
uses TCP multiplexing over a single persistent connection per worker per replica: the agent dials the server, and that
channel is then used bidirectionally, worker-to-control-plane requests and control-plane-to-worker requests
alike flow over the same tunnel. This is implemented with
[outbound](https://github.com/tardigradeproj/outbound), a library which creates local addresses for remote systems.

**[outbound](https://github.com/tardigradeproj/outbound) does not cover identity.** It provides the multiplexed transport layer but has no notion of who
is on the other end. Authentication is handled separately via mutual TLS: each agent presents a client
certificate when dialing the server, and the server verifies it against the cluster CA before accepting the
connection. The agent's identity is read from the certificate's Common Name field (for example
`CN=system:node:worker2`), so the server knows exactly which node owns the tunnel without any out-of-band
signalling.

### Agent Connections

Each worker node agent maintains one tunnel to **every** plane tunnel replica. The agent dials a single
server address and, on every connection, receives an identity payload containing a `NumberOfInstances`
field. The server resolves this count at request time by performing a DNS lookup against the plane tunnel
headless Service (configured via `--replica-discovery-dns`). If DNS resolution fails or the flag is
omitted the server returns `1`. The agent treats the received count as the desired number of concurrent
tunnels, all dialled against the same server address, and reconnects as needed to keep the count satisfied.
Each tunnel is identified by a server-assigned UUID; duplicate connections are detected and dropped before
they are registered.

Because every replica holds a tunnel for every worker, any replica can serve any kubelet request. No
per-request routing table is required and there is no wrong-replica scenario: a Service VIP in front of
the plane tunnel deployment is sufficient, and the load balancer may direct traffic to any pod.

This design retains an N×M connection count (100 workers × 3 replicas = 300 multiplexed TCP connections),
the same as konnectivity. The key differences are:

* Connections are multiplexed over a single TCP socket per worker-replica pair, so per-request overhead
  is minimal.
* Adding a replica does not force existing tunnels to drop. As new replicas start the headless Service
  gains additional DNS records; the next identity response carries the updated count and agents open the
  additional connections without interrupting existing traffic.
* No `--server-count` equivalent is required; replica count is discovered dynamically via DNS on the server side.

### Request Routing

The full request flow is:

```
API server → (HTTP CONNECT worker2:10250, mTLS) → plane tunnel Service → (agent tunnel) → kubelet
```

**Prerequisite — egress selector.**
The API server must be configured with `--egress-selector-config-file` pointing to an
`EgressSelectorConfiguration` manifest that routes `cluster` egress (kubelet traffic) through the plane
tunnel Service using the `HTTPConnect` proxy protocol with mTLS:

```yaml
apiVersion: apiserver.k8s.io/v1beta1
kind: EgressSelectorConfiguration
egressSelections:
- name: cluster
  connection:
    proxyProtocol: HTTPConnect
    transport:
      tcp:
        url: https://plane-tunnel.<namespace>.svc:8444
        tlsConfig:
          caBundle: /path/to/cluster-ca.crt
          clientKey: /path/to/apiserver-kubelet-client.key
          clientCert: /path/to/apiserver-kubelet-client.crt
```

**Step 1 — API server issues HTTP CONNECT.**
When the API server needs to reach `worker2:10250`, the egress selector intercepts the connection and
sends an HTTP CONNECT request over the mTLS channel to the plane tunnel Service:

```
CONNECT worker2:10250 HTTP/1.1
Host: worker2:10250
```

The load balancer routes this to any available plane tunnel replica.

**Step 2 — Plane tunnel authenticates and routes.**
The plane tunnel terminates the outer mTLS connection, verifying that the client certificate is signed by
the cluster CA. It then reads the CONNECT target hostname (`worker2`), looks up the agent tunnel
registered under that name, and dials the kubelet upstream through it.

Once the tunnel is established, the plane tunnel responds:

```
HTTP/1.1 200 Connection Established
```

**Step 3 — End-to-end TLS between API server and kubelet.**
After the `200` response, the API server performs its real TLS handshake directly with the kubelet
through the established tunnel. The plane tunnel does not terminate this inner TLS session: it splices
bytes between the mTLS channel and the agent tunnel. The API server verifies the kubelet's serving
certificate, and the kubelet verifies the API server's client certificate, exactly as in a direct
connection.

---

## Properties

**Authentication on every leg.**
The agent→plane-tunnel leg is authenticated via mTLS using the node's `system:node:<name>` client
certificate. The API server→plane-tunnel leg is authenticated via mTLS using the API server's kubelet
client certificate, both verified against the cluster CA. No unauthenticated path exists.

**End-to-end TLS between API server and kubelet.**
The plane tunnel authenticates and routes the outer CONNECT channel but does not terminate the inner
TLS session. The API server verifies the kubelet's serving certificate, and the kubelet verifies the
API server's client certificate, exactly as in a direct connection. TLS verification is unaffected by
node IP changes, certificate rotation, or any mismatch between `node.Status.Addresses` and the certificate.

**No routing table required.**
Because every replica holds a tunnel for every worker, any replica can serve any request. The load
balancer in front of the plane tunnel Service may distribute connections freely. No `/etc/hosts`
management, no sidecar agent in the API server pod, and no per-replica routing reconciliation is needed.

**Graceful scale-out.**
When a new plane tunnel replica starts, the headless Service gains an additional DNS record. The next
identity response any agent receives carries the updated `NumberOfInstances` count; agents open the
additional connection without interrupting existing traffic. No flag equivalent to konnectivity's
`--server-count` is required.

**N×M connection count.**
The design sustains one multiplexed TCP connection per worker per replica (100 workers × 3 replicas =
300 connections). This is the same count as konnectivity, but connections are multiplexed: all requests
from a given worker to a given replica share one TCP socket, so per-request overhead is minimal and file
descriptor consumption is bounded by the worker and replica counts rather than by request rate.

**No iptables.**
The design requires no iptables rules in any network namespace. Routing is handled entirely at the
application layer: the HTTP CONNECT target hostname identifies the worker, and the plane tunnel looks
it up in its in-memory tunnel registry.

**No dynamic port binding.**
The plane tunnel never binds a per-worker port. There is one listener on port 8443 for agent tunnels,
one on port 8444 for API server egress, and an in-memory map from worker hostname to agent tunnel.
Port exhaustion is not a concern.

**No infrastructure changes required.**
The design requires no changes to the cluster CNI, no dedicated IP CIDRs, no secondary pod IPs, and no
host-level routing configuration. It works on any standard Kubernetes cluster.

**API server changes are limited to two flags.**
The API server requires `--kubelet-preferred-address-types=Hostname` and
`--egress-selector-config-file`. Beyond those two flags, the API server is unaware of the tunnel
infrastructure: it issues standard kubelet requests and the egress selector handles routing and
authentication transparently.
