#!/bin/sh
# Removes all processes, network state, and files created by pkg/provision/worker/.
# Paths match the defaults in pkg/provision/worker/typ/type.go.
set -e

CONTAINERD_SOCK=/run/heir/containerd.sock
CRICTL=/var/lib/heir/bin/crictl

# --- stop and disable heir systemd service ---
systemctl stop heir 2>/dev/null || true
systemctl disable heir 2>/dev/null || true
rm -f /etc/systemd/system/heir.service
systemctl daemon-reload 2>/dev/null || true

# --- drain all pods and containers before tearing down containerd ---
if [ -x "$CRICTL" ] && [ -S "$CONTAINERD_SOCK" ]; then
    "$CRICTL" --runtime-endpoint "unix://$CONTAINERD_SOCK" rmp --force --all 2>/dev/null || true
fi

# --- kill worker processes: SIGTERM first, then SIGKILL after grace period ---
pkill -x heir  2>/dev/null || true
pkill -x kubelet     2>/dev/null || true
pkill -x containerd  2>/dev/null || true
pkill -x kube-proxy  2>/dev/null || true
pkill -x kube-proxy  2>/dev/null || true
sleep 2
pkill -9 -x heir              2>/dev/null || true
pkill -9 -x kubelet                 2>/dev/null || true
pkill -9 -x containerd              2>/dev/null || true
pkill -9 -x containerd-shim-runc-v2 2>/dev/null || true

# --- unmount overlay/shm/tmpfs filesystems left by containerd and kubelet ---
# Sort in reverse so child mounts are unmounted before their parents.
awk '{print $2}' /proc/mounts \
    | grep -E '^(/run/heir|/var/lib/heir)' \
    | sort -r \
    | while read -r mnt; do
        umount -l "$mnt" 2>/dev/null || true
    done

# --- delete virtual network interfaces created by CNI and flannel ---
for iface in cni0 flannel.1 kube-ipvs0; do
    ip link delete "$iface" 2>/dev/null || true
done

# --- reset iptables: flush all rules and remove all user-defined chains ---
# Clears KUBE-* chains (kube-proxy) and CNI-* chains (flannel/CNI plugins).
for table in filter nat mangle raw; do
    iptables -t "$table" -F 2>/dev/null || true
    iptables -t "$table" -X 2>/dev/null || true
done
for chain in INPUT FORWARD OUTPUT; do
    iptables -P "$chain" ACCEPT 2>/dev/null || true
done

# --- heir binary ---
rm -f /usr/local/bin/heir

# --- all heir state under /etc and /var ---
# Covers: kubelet state+PKI+kubeconfigs, static pod manifests, node profile,
#         kubelet config file, extracted binaries (kubelet/containerd/runc/crictl/shim),
#         containerd data root, containerd config, and all logs.
rm -rf /etc/heir
rm -rf /etc/lib/heir
rm -rf /var/lib/heir
rm -rf /var/log/heir

# --- containerd socket and runtime state (already unmounted above) ---
rm -rf /run/heir

# --- CNI plugin binaries and network configuration written by flannel ---
rm -rf /opt/cni/bin
rm -rf /etc/cni/net.d

# --- konnectivity server UDS socket directory ---
rm -rf /etc/kubernetes/konnectivity-server

echo "worker node cleanup complete"
