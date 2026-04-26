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

FROM debian:bookworm-slim

ARG TARGETOS
ARG TARGETARCH

RUN DEBIAN_FRONTEND=noninteractive apt update \
 && apt install -y procps ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=builder /workspace/kuke-${TARGETOS}-${TARGETARCH} /bin/kuke
RUN ln /bin/kuke /bin/kukeond
RUN chmod 0755 /bin/kuke /bin/kukeond

CMD ["/bin/kukeond"]
