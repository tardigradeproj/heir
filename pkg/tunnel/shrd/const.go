package shrd

// IdentityUpstreamID is the upstream ID workers dial to retrieve this
// plane-tunnel instance's stable identity.
const IdentityUpstreamID uint8 = 0

// KubeletUpstreamID is the outbound upstream ID the node agent registers for its local kubelet.
const KubeletUpstreamID uint8 = 1
