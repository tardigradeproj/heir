# Plane Tunnel — Operations Guide

This guide covers certificate setup and running the `tunnel server` and `tunnel agent` binaries
on bare metal or a VM. Read [plane-tunnel.md](plane-tunnel.md) for design background.

---

## Overview

Two processes run:

| Process | Host | Purpose |
|---|---|---|
| `tunnel server` | Management host | Accepts agent tunnels (`:8443`) and API server CONNECT requests (`:8444`) |
| `tunnel agent` | Each worker node | Dials the server, registers the node's kubelet as an upstream |

Both sides authenticate with mutual TLS against a shared cluster CA.

---

## Certificate Setup

Three sets of certificates are needed:

1. A **cluster CA** — signs everything
2. A **server certificate** — used by the tunnel server on both its listeners
3. A **node certificate** per worker — used by each agent to identify itself

All commands below are run from the repository root. Generated files land in `pki/`, which is
gitignored.

### 1. Create the cluster CA

```bash
mkdir -p pki

openssl genrsa -out pki/ca.key 4096

openssl req -x509 -new -nodes \
  -key pki/ca.key \
  -sha256 -days 3650 \
  -subj "/CN=heir-ca" \
  -out pki/ca.crt
```

### 2. Create the server certificate

The server certificate must have a SAN that matches the address agents and the API server
use to reach it.

```bash
mkdir -p pki/server

openssl genrsa -out pki/server/server.key 2048

# Adjust IP.1 to the address your agents will dial
cat > /tmp/server.ext <<EOF
subjectAltName = IP:127.0.0.1
EOF

openssl req -new \
  -key pki/server/server.key \
  -subj "/CN=plane-tunnel" \
  -out /tmp/server.csr

openssl x509 -req \
  -in /tmp/server.csr \
  -CA pki/ca.crt \
  -CAkey pki/ca.key \
  -CAcreateserial \
  -days 365 \
  -extfile /tmp/server.ext \
  -out pki/server/server.crt
```

Verify:

```bash
openssl verify -CAfile pki/ca.crt pki/server/server.crt
```

### 3. Create a node certificate (repeat per worker)

The Common Name must be `system:node:<hostname>`. The server uses this CN to identify which
worker owns the tunnel.

```bash
export NODE=worker1   # set to the actual node hostname

mkdir -p pki/nodes/${NODE}

openssl genrsa -out pki/nodes/${NODE}/kubelet.key 2048

openssl req -new \
  -key pki/nodes/${NODE}/kubelet.key \
  -subj "/O=system:nodes/CN=system:node:${NODE}" \
  -out /tmp/kubelet.csr

openssl x509 -req \
  -in /tmp/kubelet.csr \
  -CA pki/ca.crt \
  -CAkey pki/ca.key \
  -CAcreateserial \
  -days 365 \
  -out pki/nodes/${NODE}/kubelet.crt
```

Verify:

```bash
openssl verify -CAfile pki/ca.crt pki/nodes/${NODE}/kubelet.crt
openssl x509 -in pki/nodes/${NODE}/kubelet.crt -noout -subject
# subject=O=system:nodes, CN=system:node:worker1
```

Copy `pki/ca.crt` and `pki/nodes/${NODE}/kubelet.{crt,key}` to the worker node.

---

## Running the Server

Start by building the binary

```bash
go build cmd/tunnel.go
```

```bash
./tunnel server \
  --tunnel-cert     pki/server/server.crt \
  --tunnel-key      pki/server/server.key \
  --tunnel-ca-cert  pki/ca.crt \
  --tunnel-addr     :8443 \
  --egress-cert     pki/server/server.crt \
  --egress-key      pki/server/server.key \
  --egress-ca-cert  pki/ca.crt \
  --egress-addr     :8444
```

The `--tunnel-*` flags configure the listener that accepts agent connections. The `--egress-*`
flags configure the listener that accepts HTTP CONNECT requests from the API server. Both use
the same certificate in this example.

The optional `--replica-discovery-dns` flag controls how many replicas agents are told to
connect to. Pass the headless Service DNS name for the tunnel server deployment and the
server will resolve it on every identity request to count the current pod IPs:

```bash
./tunnel server \
  --tunnel-cert     pki/server/server.crt \
  --tunnel-key      pki/server/server.key \
  --tunnel-ca-cert  pki/ca.crt \
  --egress-cert     pki/server/server.crt \
  --egress-key      pki/server/server.key \
  --egress-ca-cert  pki/ca.crt \
  --replica-discovery-dns plane-tunnel-headless.<namespace>.svc.cluster.local
```

If the flag is omitted or DNS resolution fails the server reports `NumberOfInstances: 1`
and logs a warning. Agents tolerate this gracefully: they simply target one connection
instead of the full replica count.

All flags can be supplied as environment variables instead:

| Flag | Environment variable |
|---|---|
| `--tunnel-cert` | `TUNNEL_SERVER_TUNNEL_CERT` |
| `--tunnel-key` | `TUNNEL_SERVER_TUNNEL_KEY` |
| `--tunnel-ca-cert` | `TUNNEL_SERVER_TUNNEL_CA_CERT` |
| `--tunnel-addr` | `TUNNEL_SERVER_TUNNEL_ADDR` |
| `--egress-cert` | `TUNNEL_SERVER_EGRESS_CERT` |
| `--egress-key` | `TUNNEL_SERVER_EGRESS_KEY` |
| `--egress-ca-cert` | `TUNNEL_SERVER_EGRESS_CA_CERT` |
| `--egress-addr` | `TUNNEL_SERVER_EGRESS_ADDR` |
| `--keep-alive` | `TUNNEL_SERVER_KEEP_ALIVE` |
| `--replica-discovery-dns` | `TUNNEL_SERVER_REPLICA_DISCOVERY_DNS` |

---

## Running the Agent

Run this on each worker node. The only required flag is `--server-addr`; the certificate paths
default to the Heir standard locations shown below.


```bash
./tunnel agent \
  --cert         pki/nodes/worker1/kubelet.crt \
  --key          pki/nodes/worker1/kubelet.key \
  --ca-cert      pki/ca.crt \
  --server-addr  127.0.0.1:8443 \
  --kubelet-addr 127.0.0.1:10250
```

| Flag | Default |
|---|---|
| `--cert` | `/etc/heir/kubelet/pki/kubelet.crt` |
| `--key` | `/etc/heir/kubelet/pki/kubelet.key` |
| `--ca-cert` | `/etc/heir/pki/ca.crt` |
| `--kubelet-addr` | `127.0.0.1:10250` |
| `--keep-alive` | `12s` |

All flags accept environment variable overrides:

| Flag | Environment variable |
|---|---|
| `--cert` | `TUNNEL_AGENT_CERT` |
| `--key` | `TUNNEL_AGENT_KEY` |
| `--ca-cert` | `TUNNEL_AGENT_CA_CERT` |
| `--server-addr` | `TUNNEL_AGENT_SERVER_ADDR` |
| `--kubelet-addr` | `TUNNEL_AGENT_KUBELET_ADDR` |
| `--keep-alive` | `TUNNEL_AGENT_KEEP_ALIVE` |

---

## Verifying the Setup

**Server TLS is reachable** — run this from a worker node, substituting the management host address:

```bash
openssl s_client \
  -connect <management-host>:8443 \
  -CAfile  pki/ca.crt \
  -cert    pki/nodes/worker1/kubelet.crt \
  -key     pki/nodes/worker1/kubelet.key \
  </dev/null 2>&1 | grep -E "Verify return|subject"
```

Expected:

```
depth=1 CN=heir-ca
verify return:1
depth=0 CN=plane-tunnel
verify return:1
```

**Agent is connected** — the agent logs at `INFO` level once each connection is registered:

```
level=info msg="connection established" connections=1 target=1
```

`connections` reaching `target` means the agent is fully meshed.
