FROM quay.io/k0sproject/bootloose-ubuntu24.04

ARG KUBE_VERSION=v1.34.0
ARG TARGETARCH=arm64

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates iptables && \
    curl -fsSL "https://dl.k8s.io/release/${KUBE_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
        -o /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl && \
    apt-get clean && rm -rf /var/lib/apt/lists/*
