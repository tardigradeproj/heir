ARG BASE_IMAGE=unknwon
ARG TARGETARCH
FROM ${BASE_IMAGE}-${TARGETARCH}

ARG TARGETPLATFORM
ARG TARGETARCH


COPY artifacts/controlplane/${TARGETARCH}/kubectl  /usr/local/bin/kubectl
COPY artifacts/controlplane/${TARGETARCH}/kube-apiserver  /usr/local/bin/kube-apiserver
COPY artifacts/controlplane/${TARGETARCH}/kube-controller-manager   /usr/local/bin/kube-controller-manager
COPY artifacts/controlplane/${TARGETARCH}/kube-scheduler   /usr/local/bin/kube-scheduler
COPY ${TARGETPLATFORM}/masteragent /usr/local/bin/masteragent


ENTRYPOINT ["/init"]