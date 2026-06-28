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
channel is then used bidirectionally, worker-to-control-plane requests and control-plane-to-worker requests
alike flow over the same tunnel. This is implemented with
[outbound](https://github.com/tardigradeproj/outbound), which creates local addresses for remote systems.

**[outbound](https://github.com/tardigradeproj/outbound) does not cover identity.** It provides the multiplexed transport layer but has no notion of who
is on the other end. Authentication is handled separately via mutual TLS: each agent presents a client
certificate when dialing the server, and the server verifies it against the cluster CA before accepting the
connection. The agent's identity is read from the certificate's Common Name field, for example,
`CN=system:node:worker2`, so the server knows exactly which node owns the tunnel without any out-of-band
signalling.

The proposal also removes the N×M connection cost. Using Kubernetes Leases, each server instance watches
lease objects via an informer and builds a local routing index from them. When a request from the API server
arrives at a server instance that does not hold the target worker's tunnel, the instance looks up the lease
for that worker, finds the Pod IP of the peer that owns the connection, and forwards the request there.

When an agent connects, the server it lands on writes a Lease advertising ownership of that node:

```yaml
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: worker2                      # Matches the node name from CN=system:node:worker2
  namespace: abc
spec:
  holderIdentity: "10.4.2.15"       # Pod IP of the server instance holding this agent's tunnel
  leaseDurationSeconds: 15          # Expires if the server stops renewing (agent disconnected or server crashed)
  renewTime: "2026-06-28T14:52:20Z" # Heartbeated by the server for as long as the agent tunnel is live
```

All server instances watch the same namespace with a shared informer and maintain an in-memory routing index:

```go
type AgentLeaseIndex struct {
    mu    sync.RWMutex
    index map[string]string // nodeName → server Pod IP that owns the agent tunnel
}

// Lookup returns the Pod IP of the peer server holding the tunnel for nodeName.
// Returns ErrNotFound if no live lease exists for that node.
func (idx *AgentLeaseIndex) Lookup(nodeName string) (string, error)
```

Informer event handlers call `index.Add`, `index.Update`, and `index.Delete` as leases are created,
renewed, or expire. The index is lightweight: a lease is roughly 500 bytes, so 5,000 nodes consume
≈ 2.5 MB — well within a normal pod's memory budget.

### Inter-Server Forwarding

When a request from the API server arrives at a server instance that does not hold the target worker's
tunnel, the instance looks up the node name in its routing index to find the peer Pod IP, then forwards
the raw request to that peer over an HTTP channel. The receiving peer already has a local
`outbound` address representing the agent's tunnel, so it proxies the request through as if the
connection had arrived locally. From the API server's perspective, the request is fulfilled transparently
regardless of which server replica it hits.

### RBAC

The server's `ServiceAccount` on the Management Cluster needs permission to manage leases in the
tracking namespace:

```yaml
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```