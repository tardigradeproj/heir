FROM quay.io/k0sproject/bootloose-ubuntu24.04

ARG KUBERNETES_VERSION=v1.34.6+k3s1

# Install Docker
RUN apt-get update && \
    apt-get install -y ca-certificates curl gnupg lsb-release && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg && \
    chmod a+r /etc/apt/keyrings/docker.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
        > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin && \
    ln -sf /lib/systemd/system/docker.service /etc/systemd/system/multi-user.target.wants/docker.service

# Install k3s
RUN curl -sfL https://get.k3s.io | \
    INSTALL_K3S_VERSION=${KUBERNETES_VERSION} \
    INSTALL_K3S_EXEC="--disable=traefik" \
    INSTALL_K3S_SKIP_ENABLE=true \
    INSTALL_K3S_SKIP_START=true \
    sh - && \
    ln -sf /etc/systemd/system/k3s.service /etc/systemd/system/multi-user.target.wants/k3s.service

CMD ["/bin/bash"]
