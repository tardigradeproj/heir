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

### Request Routing

Each server instance only holds the tunnels for the agents that happened to connect to it. When the API
server sends a request targeting a specific node, for example, to stream logs from a pod, it connects
to the worker node's real IP address. The routing problem is: that IP is not directly reachable from the
Management Cluster, and the tunnel for that node may be held by any of the plane tunnel replicas.

The solution operates entirely at the network layer, keeping the API server itself completely unaware of
the tunnel infrastructure.

All plane tunnel instances are exposed behind a **headless Service**. A headless Service has no ClusterIP;
DNS resolves it directly to the Pod IPs of all ready instances.

**Master agent** runs as a sidecar container in the API server pod. Because all containers in a pod share
the same network namespace, rules programmed by master agent affect the API server's traffic transparently.

The full request flow is:

```
API server → (OUTPUT DNAT) → plane tunnel pod:dynamic-port → (PREROUTING REDIRECT) → plane tunnel listener:10250 → agent tunnel → kubelet on node X
```

**Step 1 — Plane tunnel assigns a dynamic port per agent.**
When an agent connects and its identity is verified via mTLS, the plane tunnel assigns it a unique port
number from the range 30000–30500. No socket is ever bound to this port — it is a pure logical label, a
number that exists only in a lookup table mapping port → agent tunnel. Each plane tunnel replica manages
its own port pool independently; because master agent targets replica Pod IPs directly, the same port
number can appear on different replicas without conflict.

**Step 2 — Master agent builds the routing table.**
Master agent periodically resolves the headless Service DNS to discover all plane tunnel Pod IPs. It then
polls each instance's `/v1/report` endpoint to learn which worker nodes are connected to it and which
dynamic port each node was assigned. From these responses it builds a routing table:
worker node IP → (plane tunnel Pod IP, dynamic port).

**Step 3 — Master agent programs DNAT rules.**
For each entry in the routing table, master agent installs an iptables rule in the OUTPUT chain of the
nat table inside the API server pod's network namespace. The dynamic port in the rule is the port in the
30000–30500 range that the plane tunnel assigned to that node's agent:

```
iptables -t nat -A OUTPUT -d <node-IP> -j DNAT --to-destination <plane-tunnel-pod-IP>:<dynamic-port (30000–30500)>
```

When the API server opens a connection to a worker node's real IP, the kernel rewrites the destination
to the specific plane tunnel Pod IP and dynamic port before the packet leaves the pod. From the API
server's perspective nothing has changed — it still addressed the worker node directly.

**Step 4 — Plane tunnel redirects all incoming traffic to a single listener.**
At startup, each plane tunnel instance installs a single iptables rule in the PREROUTING chain of its
own pod network namespace:

```
iptables -t nat -A PREROUTING -p tcp --dport 30000:30500 -j REDIRECT --to-ports 10250
```

This rule intercepts every TCP connection arriving at the pod on any port in the 30000–30500 range and
redirects it to port 10250 before any socket lookup occurs. The plane tunnel maintains exactly one
listener on port 10250.

**Step 5 — Plane tunnel identifies the target node and proxies the request.**
The PREROUTING REDIRECT rule creates a conntrack entry in the plane tunnel pod's own network namespace.
When the listener on port 10250 accepts a connection, it calls `SO_ORIGINAL_DST` on the accepted socket.
Because the NAT entry being queried was created by a rule in the same network namespace as the socket,
the kernel returns the correct pre-redirect destination: the plane tunnel Pod IP and the dynamic port the
packet was addressed to when it arrived. The plane tunnel looks up which agent tunnel owns that dynamic
port and proxies the request through it. The response streams back the same way.

**Rule lifecycle.** Master agent reconciles rules on every poll cycle: it adds rules for newly connected
nodes, removes rules for nodes that have disconnected, and updates rules for nodes that have reconnected
to a different plane tunnel replica. On restart, master agent flushes any rules it owns before rebuilding
from scratch.

### Report Endpoint

Each plane tunnel instance exposes an HTTP endpoint that master agent polls to discover which worker
nodes are currently connected to it and the dynamic port assigned to each:

```
GET /v1/report
```

Response body:

```go
type ReportResponse struct {
    Nodes []ConnectedNode `json:"nodes"`
}

type ConnectedNode struct {
    Name string `json:"name"` // node name, e.g. "worker2"
    IP   string `json:"ip"`   // worker node IP, e.g. "192.168.1.10"
    Port int    `json:"port"` // dynamic port assigned to this node's tunnel, e.g. 30000
}
```

Example:

```json
{
  "nodes": [
    { "name": "worker2", "ip": "192.168.1.10", "port": 30000 },
    { "name": "worker5", "ip": "192.168.1.13", "port": 30001 }
  ]
}
```

An empty `nodes` array means the instance currently holds no agent tunnels. Master agent must remove any
DNAT rules that point to an instance for nodes no longer present in its report.

---

## Properties

Conntrack tables are per-namespace, a NAT
entry created in namespace A is invisible to a socket in namespace B. In this design, the REDIRECT rule
lives in the plane tunnel pod's own network namespace, and the socket that calls `SO_ORIGINAL_DST` is
in that same namespace. The kernel finds the conntrack entry locally and returns the correct
pre-redirect port. The DNAT installed by master agent in the API server pod's namespace plays no role
in the `SO_ORIGINAL_DST` lookup, it is already resolved and gone by the time the packet arrives at
the plane tunnel.

```go
func getOriginalDst(conn net.Conn) (*net.TCPAddr, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, errors.New("connection is not a TCP connection")
	}

	// Obtain the raw control interface of the connection
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("failed to get raw connection: %w", err)
	}

	var addr unix.RawSockaddrInet4
	var sysErr error

	// Execute getsockopt safely via Control context
	err = rawConn.Control(func(fd uintptr) {
		var len uint32 = uint32(unix.SizeofRawSockaddrInet4)
		
		// Invoke the syscall: SOL_IP = 0, SO_ORIGINAL_DST = 80
		sysErr = unix.Getsockopt(int(fd), unix.SOL_IP, SO_ORIGINAL_DST, &addr, &len)
	})

	if err != nil {
		return nil, fmt.Errorf("raw connection control error: %w", err)
	}
	if sysErr != nil {
		return nil, fmt.Errorf("getsockopt SO_ORIGINAL_DST failed: %w", sysErr)
	}

	// Parse the network byte order (Big Endian) results
	ip := net.IP(addr.Addr[:])
	
	// Port mapping logic from big endian byte array
	port := int(addr.Port[0])<<8 + int(addr.Port[1])

	return &net.TCPAddr{IP: ip, Port: port}, nil
}
```

**No dynamic port binding.**
The dynamic port assigned to each agent is never bound to a socket. It is a number that exists in two
places: the plane tunnel's in-memory lookup table and the DNAT rule installed by master agent. The plane
tunnel never calls `listen()` on it. All connections, regardless of which dynamic port they were
addressed to, arrive at the same listener on port 10250. This eliminates the risk of port exhaustion on
the plane tunnel side and keeps the implementation simple: one listener, one accept loop.

**No port coordination across replicas.**
Because master agent resolves the headless Service and targets each replica by its Pod IP directly, two
replicas can assign the same dynamic port number to different agents without any conflict. The DNAT rule
encodes both the Pod IP and the port, so packets always reach the correct replica. Replicas are fully
independent and require no shared state for port allocation.

**One DNAT rule per worker node.**
Master agent installs exactly one rule per connected worker node, regardless of how many services or
ports the API server may use to communicate with that node. The rule matches on the destination IP only
and rewrites it to the appropriate plane tunnel Pod IP and dynamic port. Rule count scales linearly with
the number of worker nodes and does not grow with the number of services or ports in use.

**No infrastructure changes required.**
The design requires no changes to the cluster CNI, no dedicated IP CIDRs, no secondary pod IPs, no
BGP route advertisement, and no host-level routing configuration. It works on any standard Kubernetes
cluster using only iptables rules installed within existing pod network namespaces, which are already
available without elevated host privileges.

**Full API server transparency.**
The API server connects to worker node IPs directly, as it would in a traditional deployment. The entire
tunnel mechanism, DNAT, REDIRECT, SO_ORIGINAL_DST, agent tunnel multiplexing operates below the
application layer and is invisible to the API server. No changes to API server configuration or behavior
are required.
