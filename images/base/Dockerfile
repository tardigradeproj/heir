FROM ubuntu:22.04
ARG S6_OVERLAY_VERSION=3.2.2.0

RUN apt-get update && apt-get install -y --no-install-recommends curl xz-utils ca-certificates \
    && S6_ARCH=$(uname -m) \
    && curl -sL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" | tar -C / -Jxpf - \
    && curl -sL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" | tar -C / -Jxpf - \
    && apt-get remove -y xz-utils \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Create service AND log-service directories, plus the manifests directory where
# the <runtime>-config ConfigMap entries will be mounted at runtime.
RUN mkdir -p \
    /etc/kubernetes/manifests \
    /etc/kubernetes/manifests.d \
    /etc/s6-overlay/s6-rc.d/kine/dependencies.d \
    /etc/s6-overlay/s6-rc.d/kine-log \
    /etc/s6-overlay/s6-rc.d/kube-apiserver/dependencies.d \
    /etc/s6-overlay/s6-rc.d/kube-apiserver-log \
    /etc/s6-overlay/s6-rc.d/kube-controller-manager/dependencies.d \
    /etc/s6-overlay/s6-rc.d/kube-controller-manager-log \
    /etc/s6-overlay/s6-rc.d/kube-scheduler/dependencies.d \
    /etc/s6-overlay/s6-rc.d/kube-scheduler-log \
    /etc/s6-overlay/s6-rc.d/user/contents.d

# ==========================================
# Service: apply-manifests (Oneshot)
# ==========================================
COPY --chmod=700 bootstrap.sh /usr/local/bin/bootstrap.sh

COPY <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller/type
longrun
EOF

RUN touch /etc/s6-overlay/s6-rc.d/bootstrap-controller/dependencies.d/kube-apiserver

COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller/run
#!/command/execlineb -P
fdmove -c 2 1
/usr/local/bin/bootstrap.sh
EOF

RUN touch /etc/s6-overlay/s6-rc.d/user/contents.d/bootstrap-controller

# ==========================================
# LOG: bootstrap-controller-log
# ==========================================
COPY <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller/producer-for
bootstrap-controller-log
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller-log/type
longrun
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller-log/consumer-for
bootstrap-controller
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller-log/pipeline-name
bootstrap-controller-pipeline
EOF

COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/bootstrap-controller-log/run
#!/command/execlineb -P
foreground { s6-mkdir -p /var/log/bootstrap-controller }
# Added 'T' for timestamps
s6-log T n3 s5000000 /var/log/bootstrap-controller
EOF
# ==========================================
# Service & Pipeline: kine
# ==========================================
COPY <<EOF /etc/s6-overlay/s6-rc.d/kine/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kine/producer-for
kine-log
EOF
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kine/run
#!/command/execlineb -P
fdmove -c 2 1
/etc/kubernetes/manifests/kine.sh
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/kine-log/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kine-log/consumer-for
kine
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kine-log/pipeline-name
kine-pipeline
EOF
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kine-log/run
#!/command/execlineb -P
foreground { s6-mkdir -p /var/log/kine }
s6-log n3 s5000000 /var/log/kine
EOF


# ==========================================
# Service & Pipeline: kube-apiserver
# ==========================================
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver/producer-for
kube-apiserver-log
EOF
RUN touch /etc/s6-overlay/s6-rc.d/kube-apiserver/dependencies.d/kine
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver/run
#!/command/execlineb -P
fdmove -c 2 1
/etc/kubernetes/manifests/kube-apiserver.sh
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver-log/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver-log/consumer-for
kube-apiserver
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver-log/pipeline-name
kube-apiserver-pipeline
EOF
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-apiserver-log/run
#!/command/execlineb -P
foreground { s6-mkdir -p /var/log/kube-apiserver }
s6-log n3 s5000000 /var/log/kube-apiserver
EOF

# ==========================================
# Service & Pipeline: kube-controller-manager
# ==========================================
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager/producer-for
kube-controller-manager-log
EOF
RUN touch /etc/s6-overlay/s6-rc.d/kube-controller-manager/dependencies.d/kube-apiserver
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager/run
#!/command/execlineb -P
fdmove -c 2 1
/etc/kubernetes/manifests/kube-controller-manager.sh
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager-log/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager-log/consumer-for
kube-controller-manager
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager-log/pipeline-name
kube-controller-manager-pipeline
EOF
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-controller-manager-log/run
#!/command/execlineb -P
foreground { s6-mkdir -p /var/log/kube-controller-manager }
s6-log n3 s5000000 /var/log/kube-controller-manager
EOF

# ==========================================
# Service & Pipeline: kube-scheduler
# ==========================================
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler/producer-for
kube-scheduler-log
EOF
RUN touch /etc/s6-overlay/s6-rc.d/kube-scheduler/dependencies.d/kube-apiserver
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler/run
#!/command/execlineb -P
fdmove -c 2 1
/etc/kubernetes/manifests/kube-scheduler.sh
EOF

COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler-log/type
longrun
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler-log/consumer-for
kube-scheduler
EOF
COPY <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler-log/pipeline-name
kube-scheduler-pipeline
EOF
COPY --chmod=700 <<EOF /etc/s6-overlay/s6-rc.d/kube-scheduler-log/run
#!/command/execlineb -P
foreground { s6-mkdir -p /var/log/kube-scheduler }
s6-log n3 s5000000 /var/log/kube-scheduler
EOF

# ==========================================
# Final Registration
# ==========================================
RUN touch /etc/s6-overlay/s6-rc.d/user/contents.d/kube-controller-manager \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/kube-scheduler \
    && touch /etc/s6-overlay/s6-rc.d/user/contents.d/bootstrap-controller

ENTRYPOINT ["/init"]
