ARG BASE_IMAGE=debian:bookworm-slim
FROM $BASE_IMAGE AS base

# copy in static files (if you still have custom systemd services to inject)
COPY --chmod=0755 files/etc/systemd/system/ /etc/systemd/system/
COPY --chmod=0755 files/usr/local/bin/* /usr/local/bin/
# Install core dependencies for systemd
RUN echo "Installing Packages ..." \
    && DEBIAN_FRONTEND=noninteractive clean-install \
      systemd \
      iproute2 util-linux mount kmod \
      bash ca-certificates curl jq procps \
    && find /lib/systemd/system/sysinit.target.wants/ -name "systemd-tmpfiles-setup.service" -delete \
    && rm -f /lib/systemd/system/multi-user.target.wants/* \
    && rm -f /etc/systemd/system/*.wants/* \
    && rm -f /lib/systemd/system/local-fs.target.wants/* \
    && rm -f /lib/systemd/system/sockets.target.wants/*udev* \
    && rm -f /lib/systemd/system/sockets.target.wants/*initctl* \
    && rm -f /lib/systemd/system/basic.target.wants/* \
    && echo "ReadKMsg=no" >> /etc/systemd/journald.conf \
    && ln -s "$(which systemd)" /sbin/init

# NOTE: systemd-binfmt.service will register things into binfmt_misc which is kernel-global
RUN echo "Enabling / Disabling services ... " \
    && systemctl mask systemd-binfmt.service

# squash down to one compressed layer, without any lingering whiteout files etc
FROM scratch
COPY --from=base / /

# add metadata, must be done after the squashing
# first tell systemd that it is in docker (it will check for the container env)
# https://systemd.io/CONTAINER_INTERFACE/
ENV container=docker

# systemd exits on SIGRTMIN+3, not SIGTERM (which re-executes it)
# https://bugzilla.redhat.com/show_bug.cgi?id=1201657
STOPSIGNAL SIGRTMIN+3

# NOTE: this is *only* for documentation, the entrypoint is overridden later
ENTRYPOINT [ "/usr/local/bin/entrypoint", "/sbin/init" ]