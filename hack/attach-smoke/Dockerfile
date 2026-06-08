# Minimal busybox image for dev-init's attach smoke (the
# dev-init-attach/ds/dks/cattach cell). Built locally into the attach realm
# by scripts/dev-init.sh and tagged kukeon.internal/attach-smoke, mirroring
# the kukeon.internal/kukeond local-build convention.
#
# Based on the multi-arch docker.io/library/busybox so `kuke build` resolves
# the build host's architecture and produces a host-arch image — fixing the
# arm64 breakage from the former single-arch-amd64 registry.eminwux.com/busybox
# pin, whose amd64 `sleep` ELF could not exec on arm64 and surfaced as an
# opaque CNI netns ENOENT (issue #1158). Building locally also drops the dev
# loop's dependency on the external registry.eminwux.com; the reserved
# kukeon.internal TLD makes a stray runtime pull fail fast instead of
# silently hitting an external host. busybox provides the /bin/sh the
# attachable container's kuketty/sbsh wrapper needs plus the `sleep` the
# root and work containers run.
FROM docker.io/library/busybox:latest
