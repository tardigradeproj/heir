# Worker Node OS Requirements

This document describes every OS-level validation and configuration change that
`heir provision worker` must verify or apply before and during node
provisioning.

Heir is a self-contained distro. It embeds all Kubernetes binaries
(kubelet, containerd, containerd-shim-runc-v2, runc, crictl, ctr) and extracts
them at join time. No `apt`, `yum`, or external package repository is needed or
allowed.

---

## 1. Validations (must be checked before proceeding)

### 1.1 Privileges

| Check | Why |
|---|---|
| Process is running as `root` (UID 0) | Required to write to `/usr/local/bin/`, `/etc/systemd/system/`, `/etc/heir/`, `/var/lib/heir/`, `/opt/cni/bin/` |

Fail early with a clear error if not root. Everything else depends on it.

---

### 1.2 Init System

| Check | Why |
|---|---|
| Init system is systemd | `setupUnits` connects to systemd via D-Bus to write, enable, and start `containerd.service` and `kubelet.service` |
| systemd D-Bus socket is reachable (`/run/systemd/private/...` or `/run/dbus/system_bus_socket`) | The `go-systemd/dbus` connection will fail silently-ish otherwise |
| systemd is not in degraded/emergency mode | Starting new units in a broken systemd state produces misleading errors |

How to check:
```
cat /proc/1/comm          # must print "systemd"
systemctl is-system-running  # must return "running" or "degraded" (warn on degraded)
```

---

### 1.3 Linux Kernel Version

| Check | Minimum | Why |
|---|---|---|
| Kernel version | 4.19+ | Required for cgroups v2 and a stable overlayfs implementation |
| cgroups v2 mounted | `/sys/fs/cgroup/cgroup.controllers` exists | kubelet and containerd are configured with `cgroupDriver: systemd`, which targets cgroups v2. On a pure v1 host the unified hierarchy must at least be available |

How to check:
```
uname -r
ls /sys/fs/cgroup/cgroup.controllers
```

Note: cgroups v1 with systemd driver is also supported but cgroups v2 is the
expected path going forward. If only v1 is available, the `SystemdCgroup = true`
option in the containerd config and `cgroupDriver: systemd` in the kubelet
config must still be compatible — validate that `/sys/fs/cgroup/systemd` is
mounted.

---

### 1.4 Required Kernel Modules

These modules must be loadable (either already loaded or available in the
running kernel):

| Module | Why |
|---|---|
| `overlay` | containerd uses overlayfs as the preferred snapshotter (`native` is the current fallback, but overlayfs is the target) |
| `br_netfilter` | Required for `bridge-nf-call-iptables` sysctl to take effect — without it the sysctl write succeeds but the kernel ignores it |

How to check:
```
lsmod | grep overlay
lsmod | grep br_netfilter
# or check if loadable:
modinfo overlay
modinfo br_netfilter
```

---

### 1.5 Kernel Networking Parameters (sysctl)

| Parameter | Required Value | Why |
|---|---|---|
| `net.ipv4.ip_forward` | `1` | Allows the kernel to forward packets between network interfaces — mandatory for pod-to-pod traffic across nodes |
| `net.bridge.bridge-nf-call-iptables` | `1` | Makes bridge traffic traverse iptables — required by kube-proxy and most CNI plugins |
| `net.bridge.bridge-nf-call-ip6tables` | `1` | Same as above for IPv6 paths |

Validation: read `/proc/sys/net/ipv4/ip_forward` and
`/proc/sys/net/bridge/bridge-nf-call-iptables`. These are applied as part of
the configuration phase (see section 2), but must be verified as effective
after application.

---

### 1.6 iptables

| Check | Why |
|---|---|
| `iptables` binary is present | kube-proxy and CNI plugins use iptables to manage pod networking rules |
| `iptables` variant (legacy vs nft) is consistent with what the kernel supports | Mixing `iptables-legacy` and `iptables-nft` on the same host causes rules from one backend to be invisible to the other |

How to check:
```
which iptables
iptables --version    # shows if it's legacy or nft
```

Note: on Ubuntu 24.04 (the current integration test image) the default is
`iptables-nft`. Verify the CNI plugin being used supports the same backend.

---

### 1.7 Swap

| Check | Why |
|---|---|
| Swap is disabled | kubelet fails to start if swap is on, unless `--fail-swap-on=false` is explicitly passed via `--kubelet-extra-args` |

How to check:
```
swapon --show    # empty output means no swap active
cat /proc/swaps
```

The integration test currently works around this with `--kubelet-extra-args="fail-swap-on=false"`. The correct long-term behaviour is to either disable swap on the node or document that `fail-swap-on=false` is required.

---

### 1.8 Port Availability

| Port | Process | Why |
|---|---|---|
| `10250/tcp` | kubelet | kubelet API — API server calls into this port for `exec`, `logs`, `metrics` |

How to check:
```
ss -tlnp | grep 10250
```

---

### 1.9 No Conflicting Processes

| Process | Conflict |
|---|---|
| An existing `containerd` process | Will hold `/var/run/containerd/containerd.sock`; the new unit will fail to start |
| An existing `kubelet` process | Will conflict with the new systemd unit |
| `dockerd` | Docker also uses containerd internally and may hold the containerd socket or conflict on cgroup management |

How to check:
```
pgrep -x containerd
pgrep -x kubelet
pgrep -x dockerd
```

---

### 1.10 Bootstrap Token / Kubeconfig

The `--token` flag accepts a base64-encoded kubeconfig, not a raw kubeadm
bootstrap token. Validate:

| Check | How |
|---|---|
| Value is valid base64 | `base64.StdEncoding.DecodeString` (already done in `saveBootstrapKubeconfig`) |
| Decoded bytes are a valid kubeconfig | `clientcmd.Load` (already done) |
| `currentContext` is set | `cfg.Contexts[cfg.CurrentContext] != nil` (already done) |
| Referenced cluster has non-empty `CertificateAuthorityData` | `cluster.CertificateAuthorityData != nil` (already done) |
| API server URL in the kubeconfig is reachable | TCP dial or HTTPS GET to the server address with the embedded CA — not yet implemented |

The API server reachability check should happen before any file is written to
disk so the node is not left in a partially provisioned state if the token is
wrong.

---

### 1.11 Filesystem and Disk Space

| Check | Why |
|---|---|
| `/usr/local/bin/` is writable | All six binaries are placed here |
| `/etc/heir/kubernetes/` can be created | kubeconfig, CA cert, containerd config |
| `/var/lib/heir/kubelet/` can be created | kubelet config and cert dir |
| `/opt/cni/bin/` can be created | CNI plugin binaries |
| `/etc/cni/net.d/` can be created | CNI configuration files |
| `/etc/systemd/system/` is writable | Unit files for `containerd.service` and `kubelet.service` |
| Enough free disk space | Binaries alone are several hundred MB; allow at least 2 GB free for binaries, container image layers, and logs |

---

### 1.12 Architecture

| Check | Why |
|---|---|
| CPU architecture matches embedded binaries | The embedded artifacts are built for a specific architecture. The containerd config currently hardcodes `platform = "linux/arm64"` (`saveContainerdConfig`). On an `amd64` host this will cause containerd to fail to unpack images |

How to check:
```
uname -m    # arm64 / aarch64 or x86_64
```

This is currently a known limitation: the platform string in
`saveContainerdConfig` must match the host architecture or be made dynamic.

---

## 2. Changes to Apply (OS configuration)

These are changes the provisioner must apply to the OS before or alongside
extracting binaries and writing configs. They are not done by the current
`Join` implementation and need to be added.

---

### 2.1 Load Kernel Modules

Load immediately and persist across reboots:

```
# Load now
modprobe overlay
modprobe br_netfilter

# Persist
cat > /etc/modules-load.d/heir.conf << EOF
overlay
br_netfilter
EOF
```

Failure to load `br_netfilter` means the sysctl values below will be accepted
by the kernel but have no effect — pod networking will silently fail.

---

### 2.2 Apply sysctl Parameters

Apply immediately and persist:

```
# Apply now
sysctl -w net.ipv4.ip_forward=1
sysctl -w net.bridge.bridge-nf-call-iptables=1
sysctl -w net.bridge.bridge-nf-call-ip6tables=1

# Persist
cat > /etc/sysctl.d/99-heir.conf << EOF
net.ipv4.ip_forward = 1
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
EOF
```

`br_netfilter` must be loaded **before** applying the bridge sysctl values or
the write to `/proc/sys/net/bridge/` will return "no such file or directory".

---

### 2.3 Disable Swap (or document the trade-off)

Option A — disable swap permanently:
```
swapoff -a
# Comment out swap entries in /etc/fstab to survive reboot
sed -i '/\bswap\b/s/^/#/' /etc/fstab
```

Option B — pass `--kubelet-extra-args="fail-swap-on=false"` at join time and
document that the node runs with swap enabled (Kubernetes behaviour with swap
is defined by the `NodeSwap` feature gate; for standard clusters option A is
recommended).

---

### 2.4 Create Required Directories

The binary extraction code (`extractStreamed`) calls `os.OpenFile` directly
on the destination path. Subdirectories are created by the config writers, but
it is cleaner to pre-create all paths in a single pass:

```
/usr/local/bin/
/etc/heir/kubernetes/pki/
/var/lib/heir/kubelet/pki/
/opt/cni/bin/
/etc/cni/net.d/
/etc/systemd/system/
```

---

### 2.5 iptables Forwarding Rules

kube-proxy and most CNI plugins program iptables, but the base `FORWARD` chain
policy must allow traffic. Set it and ensure the rule survives a restart:

```
iptables -P FORWARD ACCEPT
```

For persistence across reboots, save rules using `iptables-save` /
`iptables-restore` via a systemd unit, or rely on the CNI plugin to restore
them (Calico and Flannel both re-apply rules on start). Document which approach
is in use.

---

### 2.6 Verify containerd Socket Readiness After Start

After `containerd.service` is started by `setupUnits`, kubelet's start is
triggered immediately. If the containerd socket (`/var/run/containerd/containerd.sock`)
is not yet ready, kubelet will fail and rely on its `Restart=always` to
recover. To avoid a race condition, add a readiness poll after starting
`containerd.service` and before starting `kubelet.service`:

```
# Wait until the socket exists and is connectable
until [ -S /var/run/containerd/containerd.sock ]; do sleep 0.5; done
```

In Go code this translates to a loop with a `net.Dial("unix", socketPath)` and
a timeout (e.g. 30 seconds).

---

### 2.7 Fix the Platform String in containerd Config (Architecture)

The current `saveContainerdConfig` hardcodes `platform = "linux/arm64"`. This
must be made dynamic based on `runtime.GOARCH` so the provisioner works
correctly on both `amd64` and `arm64` hosts:

```go
platform = "linux/" + runtime.GOARCH
```

---

## 3. Summary Table

| # | Category | Type | Status |
|---|---|---|---|
| 1.1 | Root privileges | Validation | Not implemented |
| 1.2 | systemd as init system | Validation | Not implemented |
| 1.3 | Kernel version / cgroups | Validation | Not implemented |
| 1.4 | Kernel modules (overlay, br_netfilter) | Validation | Not implemented |
| 1.5 | sysctl parameters | Validation | Not implemented |
| 1.6 | iptables availability and variant | Validation | Not implemented |
| 1.7 | Swap disabled | Validation | Worked around via extra arg |
| 1.8 | Port 10250 free | Validation | Not implemented |
| 1.9 | No conflicting processes | Validation | Not implemented |
| 1.10 | Token / kubeconfig + API server reachability | Validation | Partially implemented (no reachability check) |
| 1.11 | Filesystem writable + disk space | Validation | Not implemented |
| 1.12 | Architecture match | Validation | Not implemented |
| 2.1 | Load kernel modules | OS change | Not implemented |
| 2.2 | Apply sysctl parameters | OS change | Not implemented |
| 2.3 | Disable swap | OS change | Not implemented |
| 2.4 | Create required directories | OS change | Partially (done per-path inline) |
| 2.5 | iptables FORWARD policy | OS change | Not implemented |
| 2.6 | containerd socket readiness before kubelet | OS change | Not implemented (race exists) |
| 2.7 | Dynamic platform string in containerd config | Bug fix | Not implemented |
