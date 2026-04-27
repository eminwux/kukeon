FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/
COPY internal internal/
COPY Makefile Makefile

RUN make release-build KUKEON_VERSION=${VERSION} OS=${TARGETOS} ARCHS=${TARGETARCH}

# Fetch upstream CNI plugins (bridge, host-local, loopback, etc.) into
# /opt/cni/bin/. Pinned to v1.5.1 — the Debian package installs to
# /usr/lib/cni/, which is not where kukeon's defaultCniBinDir
# (internal/cni/types.go) looks.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS cni-plugins

ARG TARGETOS
ARG TARGETARCH
ARG CNI_PLUGINS_VERSION=v1.5.1

RUN DEBIAN_FRONTEND=noninteractive apt update \
 && apt install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /opt/cni/bin \
 && CNI_PLUGINS_TGZ="cni-plugins-${TARGETOS}-${TARGETARCH}-${CNI_PLUGINS_VERSION}.tgz" \
 && CNI_PLUGINS_URL="https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_VERSION}/${CNI_PLUGINS_TGZ}" \
 && curl -fsSL -o "/tmp/${CNI_PLUGINS_TGZ}" "${CNI_PLUGINS_URL}" \
 && curl -fsSL -o "/tmp/${CNI_PLUGINS_TGZ}.sha256" "${CNI_PLUGINS_URL}.sha256" \
 && echo "$(awk '{print $1}' /tmp/${CNI_PLUGINS_TGZ}.sha256)  /tmp/${CNI_PLUGINS_TGZ}" | sha256sum -c - \
 && tar -xz -C /opt/cni/bin -f "/tmp/${CNI_PLUGINS_TGZ}" \
 && rm -f "/tmp/${CNI_PLUGINS_TGZ}" "/tmp/${CNI_PLUGINS_TGZ}.sha256"

FROM debian:bookworm-slim

ARG TARGETOS
ARG TARGETARCH

# iptables: required by the bridge plugin's ipMasq rules and by
# internal/netpolicy/enforcer's egress chains, which both shell out to
# iptables in-process from the daemon container.
RUN DEBIAN_FRONTEND=noninteractive apt update \
 && apt install -y procps ca-certificates iptables \
 && rm -rf /var/lib/apt/lists/*

COPY --from=cni-plugins /opt/cni/bin /opt/cni/bin

WORKDIR /

COPY --from=builder /workspace/kuke-${TARGETOS}-${TARGETARCH} /bin/kuke
RUN ln /bin/kuke /bin/kukeond
RUN chmod 0755 /bin/kuke /bin/kukeond

CMD ["/bin/kukeond"]
