RELEASE_DIR := release

# kukebuild lives in its own Go module (cmd/kukebuild/go.mod, issue #655) whose
# BuildKit closure is deliberately kept apart from the root module's dependency
# graph (the two have clashed before, e.g. the monolithic vs. split
# google.golang.org/genproto). The repo-root go.work wires both modules for
# editor / gopls navigation, but a workspace-union build re-couples those graphs
# and fails with an ambiguous import — the exact coupling this split removes. So
# every build / test here runs per-module (GOWORK=off), matching the
# single-module resolution CI and the Docker builder (no go.work) use. Exported
# so it reaches every recipe and the $(shell) below.
export GOWORK := off

# ----- Version sourcing -----
MODULE := $(shell GOWORK=off go list -m)

# CI can pass KUKEON_VERSION (e.g., github.ref_name). If not, derive from git.
ifndef KUKEON_VERSION
KUKEON_VERSION = $(shell git describe --tags --always --dirty --match 'v*')
endif

# ----- Docker-related Variables (use the SAME version) -----
KUKEON_REGISTRY ?= eminwux
KUKEON_IMAGE_NAME := kukeon
KUKEON_IMAGE_TAG ?= $(KUKEON_VERSION)
KUKEON_DOCKER_IMAGE := $(KUKEON_REGISTRY)/$(KUKEON_IMAGE_NAME):$(KUKEON_IMAGE_TAG)

# OCI repo path (no tag) that `kuke init` will resolve kukeond from when
# --kukeond-image is not passed. Release pipeline overrides via env.
KUKEON_IMAGE_REPO ?= ghcr.io/eminwux/kukeon

# Directory `install-dev` symlinks the dev binaries into. Override for
# rootless / non-standard PATH layouts (e.g. INSTALL_PREFIX=$HOME/.local/bin).
INSTALL_PREFIX ?= /usr/local/bin

LDFLAGS := -s -w \
	-X $(MODULE)/cmd/config.Version=$(KUKEON_VERSION) \
	-X $(MODULE)/cmd/config.KukeondImageRepo=$(KUKEON_IMAGE_REPO)

# ----- Build matrix -----
BINS = kuke kukeond kuketty kukepause kukebuild
OS = linux
ARCHS = amd64 arm64


all: clean kill $(BINS)

.PHONY: release
release: release-build

.PHONY: kuke kukeond kuketty kukepause kukebuild
kuke:
	go build \
	-o kuke \
	-ldflags="$(LDFLAGS)" \
	./cmd/

# kukeond is the same binary as kuke, dispatched by argv[0] basename.
kukeond: kuke
	ln -sf kuke kukeond

# kuketty is the in-container terminal wrapper that replaces sbsh on the OCI
# injection path (issue #165). It is deliberately a separate binary (not
# argv[0]-dispatched from kuke) so its import set stays stdlib + a small pty
# helper, controlling per-process RSS and startup time as attachable-container
# count scales — see the issue body's "Why a separate binary" note.
# Build statically, as release-build does: the wrapper is injected into
# workload images and cannot depend on the build host's libc being available.
kuketty:
	CGO_ENABLED=0 go build \
	-o kuketty \
	-ldflags="$(LDFLAGS)" \
	./cmd/kuketty/

# kukepause is the minimal PID 1 for every cell's root (pause) container
# (issue #931). It is built static (CGO_ENABLED=0, no libc) and pre-staged on
# the host by `kuke init` to /opt/kukeon/bin/kukepause, then bind-mounted into
# each root container at /.kukeon/bin/kukepause — it cannot ship inside the kukeond image like
# kuketty because root containers (including kukeond's own) exist before the
# daemon is up. The version ldflags don't apply (it imports no cmd/config), so
# it links with -s -w only.
kukepause:
	CGO_ENABLED=0 go build \
	-o kukepause \
	-ldflags="-s -w" \
	./cmd/kukepause/

# kukebuild is the native image builder that embeds BuildKit as a library
# (issue #522). It lives in its own Go module (cmd/kukebuild/go.mod, issue #655)
# so BuildKit's moby / runc / grpc closure advances independently of the root
# module (which links zero BuildKit packages). Both modules still pin the same
# `go` directive and containerd version, so kukebuild and kukeond build with one
# toolchain against one containerd. The repo-root go.work wires both modules for
# local dev.
# Built from inside its module dir so the workspace resolves the kukebuild
# module's own go.mod/go.sum; the root-binary version ldflags don't apply
# (kukebuild imports no cmd/config), so it links with -s -w only.
kukebuild:
	cd cmd/kukebuild && go build \
	-o $(CURDIR)/kukebuild \
	-ldflags="-s -w" \
	.


release-build:
	# Build for all OS and ARCH combinations
	set -e; \
	for OS in $(OS); do \
		for ARCH in $(ARCHS); do \
			GO111MODULE=on CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH \
			go build -a \
			-trimpath \
			-o kuke-$$OS-$$ARCH \
			-ldflags="$(LDFLAGS)" \
			./cmd; \
			ln -sf kuke-$$OS-$$ARCH kukeond-$$OS-$$ARCH; \
			GO111MODULE=on CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH \
			go build -a \
			-trimpath \
			-o kuketty-$$OS-$$ARCH \
			-ldflags="$(LDFLAGS)" \
			./cmd/kuketty; \
			GO111MODULE=on CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH \
			go build -a \
			-trimpath \
			-o kukepause-$$OS-$$ARCH \
			-ldflags="-s -w" \
			./cmd/kukepause; \
		done \
	done

# release-build deliberately omits kukebuild: it lives in its own Go module
# (issue #655), is not shipped inside the kukeond container image (the Dockerfile
# copies only kuke + kuketty), and is not a published release asset. Cross-build
# it from its module via `make release-build-kukebuild` when a kukebuild release
# artifact is wanted; the root Dockerfile builder (no go.work) never needs the
# BuildKit closure.
.PHONY: release-build-kukebuild
release-build-kukebuild:
	set -e; \
	for OS in $(OS); do \
		for ARCH in $(ARCHS); do \
			cd $(CURDIR)/cmd/kukebuild && \
			GO111MODULE=on CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH \
			go build -a \
			-trimpath \
			-o $(CURDIR)/kukebuild-$$OS-$$ARCH \
			-ldflags="-s -w" \
			.; \
		done \
	done

clean:
	rm -rf $(HOME)/.kukeon/run/*
	rm -rf kuke kukeond kuketty kukepause kukebuild

kill:
	(killall kukeond || true )

# `make test` covers both modules in the workspace: the root module's unit
# tests (excluding the e2e suite) and the kukebuild module's tests. `go list
# ./...` from the root lists only root-module packages — the nested kukebuild
# module is excluded — so its tests are run explicitly from its module dir.
# Split into per-module sub-targets so CI can run each as its own named step
# and a red run names the failing component (issue #696); `make test` keeps
# running both locally. GOWORK=off (exported above) reaches every sub-recipe.
.PHONY: test test-root test-race test-kukebuild
test: test-root test-kukebuild

test-root:
	go test $(shell go list ./... | grep -v /e2e)

# The daemon package runs under the race detector on its own: its Serve / Stop
# tests start and stop background loops concurrently, and a plain `go test`
# cannot see a race there (#1600).
test-race:
	go test -race ./internal/daemon/...

test-kukebuild:
	cd cmd/kukebuild && go test ./...

# The Lint workflow's formatter gate (.github/workflows/lint.yaml), callable
# locally so the two cannot drift (#1642). Formatters only (golines, gci,
# gofumpt from .golangci.yml); `fmt` walks files rather than loading packages,
# so ./... also covers the separate cmd/kukebuild module. main is kept
# fmt-clean (#1501), so any diff comes from the branch. Use golangci-lint
# v2.6.2, the `rev` pinned in .pre-commit-config.yaml: another release can
# format the same source differently. Fails on a non-zero exit (config or
# parse error) or on any diff output.
GOLANGCI_LINT ?= golangci-lint

.PHONY: fmt-check
fmt-check:
	@out="$$($(GOLANGCI_LINT) fmt --diff ./... 2>&1)"; rc=$$?; \
	if [ "$$rc" -ne 0 ] || [ -n "$$out" ]; then \
		printf '%s\n' "$$out"; \
		echo "golangci-lint formatters would change the files above. Run 'GOWORK=off $(GOLANGCI_LINT) fmt ./...' (v2.6.2) and commit the result." >&2; \
		exit 1; \
	fi

# Ref the e2e suite uses for the local kukeond image. `kukebuild` writes it
# straight into the kuke-system realm's containerd namespace before `go test`
# runs `kuke init --kukeond-image`, so the test does not depend on a published
# registry tag matching `git describe`, nor on a docker daemon. `kukeon.internal`
# is the ICANN-reserved, non-routable host every locally-built kukeon image uses
# (#1063): a stray pull fails fast instead of hitting Docker Hub.
KUKEON_E2E_IMAGE ?= kukeon.internal/kukeond:v0.0.0-e2e
# Scratch dir for the e2e image build, wiped at the start of every run. It
# lives outside the repo because the repo root is the build context.
#   - KUKEON_E2E_BUILD_ROOT: a private BuildKit state root. kukebuild's default
#     root outlives the run, but the suite's uninstall tests delete the
#     kuke-system namespace's snapshots out from under it; a reused cache then
#     fails the next build with "parent snapshot ... does not exist". A fresh
#     root trades cross-run layer caching for a build that cannot reference
#     snapshots the previous run deleted.
#   - KUKEON_E2E_IMAGE_TAR: an OCI archive of the image, exported with `ctr`
#     right after the build. The same uninstall tests drop the image along with
#     the namespace, so the harness re-imports this archive before every
#     `kuke init` instead of relying on the one build surviving.
KUKEON_E2E_STATE_DIR ?= /var/tmp/kukeon-e2e
KUKEON_E2E_BUILD_ROOT := $(KUKEON_E2E_STATE_DIR)/buildroot
KUKEON_E2E_IMAGE_TAR := $(KUKEON_E2E_STATE_DIR)/kukeond.tar

e2e: test-e2e
.PHONY: test-e2e
# Prerequisites: root, Go, standalone containerd at
# /run/containerd/containerd.sock, iproute2 (`ip`) and `ctr` on PATH. No docker
# daemon or binary is needed. scripts/e2e-preflight.sh checks all of these (plus
# the freshly built kukebuild and a free /run/kukeon-e2e-network.lock) and exits
# non-zero naming each missing piece before the image build or `go test` start.
#
# The kukeond image is built by kukebuild (BuildKit as a library, host network)
# directly into kuke-system.kukeon.io. `--kukeond-config /dev/null` pins the
# default `.kukeon.io` namespace suffix so a host /etc/kukeon/kukeond.yaml with
# a dev profile cannot redirect the image into kuke-system.dev.kukeon.io — the
# build-side twin of the KUKEOND_CONFIGURATION=/dev/null isolation below.
# `ctr images export` then snapshots it to $(KUKEON_E2E_IMAGE_TAR) for the
# harness's per-test re-import (see KUKEON_E2E_STATE_DIR above).
#
# `env -u KUKEOND_SOCKET` (rather than re-exporting an absent value) keeps the
# AC's "no global KUKEOND_SOCKET for the test run" constraint honest: any
# leftover from an interactive shell — including a parent dev-init.sh that
# pinned `/run/kukeon-dev/kukeond.sock` for the nested daemon — is stripped
# before `go test` so `kuke init --run-path <tempdir>` falls through to the
# per-runPath auto-derivation. The nested probe (`/.kukeon/bin/kuketty`,
# mirrors `scripts/dev-init.sh`) only emits a diagnostic; the unset is
# unconditional because the e2e suite's daemon-bringing tests rely on the
# per-test --run-path → socket derivation in either mode.
#
# `KUKEOND_CONFIGURATION=/dev/null` short-circuits cross-test
# contamination via `/etc/kukeon/kukeond.yaml`: the first test's `kuke init`
# brings up kukeond, which writes the auto-generated default YAML on first
# start (internal/serverconfig.WriteDefault, O_EXCL — first writer wins). That
# YAML hardcodes `spec.socket: /run/kukeon/kukeond.sock` regardless of what
# socket the daemon was launched with (see issue #581). Subsequent tests'
# `kuke init` reads it via applyServerConfiguration, calls
# `viper.Set(KUKEOND_SOCKET, "/run/kukeon/kukeond.sock")`, and trips the
# `viper.IsSet` gate in `applyRunPathImpliesKukeondSocket` — derivation is
# skipped, kukeond comes up on the wrong socket, and the test's
# `unix://<runPath>/kukeond.sock` client dial fails with "no such file or
# directory". Pointing init at `/dev/null` (serverconfig.Load handles the
# zero-byte read as an absent-doc fall-through, returning a zero-value
# document with no error) keeps the contamination out of the init read path.
#
# Issue #284 collapsed KUKE_INIT_SERVER_CONFIGURATION into KUKEOND_CONFIGURATION
# — one env var, one source of truth, the same path the daemon honours.
test-e2e: kuke kukeond kuketty kukepause kukebuild
	./scripts/e2e-preflight.sh $(CURDIR)
	@echo "Building local kukeond image $(KUKEON_E2E_IMAGE) into kuke-system for e2e"
	rm -rf $(KUKEON_E2E_STATE_DIR) && mkdir -p $(KUKEON_E2E_STATE_DIR)
	./kukebuild --tag $(KUKEON_E2E_IMAGE) --realm kuke-system --kukeond-config /dev/null \
		--root $(KUKEON_E2E_BUILD_ROOT) \
		--build-arg VERSION=v0.0.0-e2e .
	ctr --namespace kuke-system.kukeon.io images export $(KUKEON_E2E_IMAGE_TAR) $(KUKEON_E2E_IMAGE)
	@echo "Running e2e tests using binaries in project root"
	@if [ -x /.kukeon/bin/kuketty ]; then \
		echo "[nested mode: kuke init --run-path <tempdir> derives <tempdir>/kukeond.sock; parent dev cell's /run/kukeon/ socket untouched]"; \
	fi
	HOME=$(HOME) PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$(PATH) \
		E2E_BIN_DIR=$(CURDIR) \
		KUKEON_E2E_IMAGE=$(KUKEON_E2E_IMAGE) \
		KUKEON_E2E_IMAGE_TAR=$(KUKEON_E2E_IMAGE_TAR) \
		KUKEOND_CONFIGURATION=/dev/null \
		env -u KUKEOND_SOCKET go test -v ./e2e

tag:
	git tag -s v$(KUKEON_VERSION) -m "Release version $(KUKEON_VERSION)"
	git push origin v$(KUKEON_VERSION)

.PHONY: dev-init
dev-init:
	./scripts/dev-init.sh

# install-dev symlinks the in-tree dev binaries into INSTALL_PREFIX so
# contributors can invoke `kuke` / `kukeond` from anywhere on the host after
# `make dev-init`. Symlinks (not copies) so subsequent `make kuke` rebuilds
# are picked up automatically — a stale hard copy is exactly the footgun a
# dev workflow can't afford. argv[0] dispatch resolves `kukeond` to the
# daemon entrypoint because the basename of the exec path is `kukeond`.
#
# kukebuild and kukepause are symlinked only when they have been built (the
# `[ -f ]` guards), so `kuke build` resolves kukebuild on PATH and `kuke init`
# resolves kukepause on PATH after `make dev-init` (which runs `make kuke
# kukepause kukebuild` before this target) without imposing their builds on a
# plain `make install-dev`. Routing them through INSTALL_PREFIX here keeps a
# single PATH-placement mechanism instead of a second one in dev-init.sh.
.PHONY: install-dev uninstall-dev
install-dev: kuke
	ln -sf kuke kukeond
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kuke
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kukeond
	@[ -f $(CURDIR)/kukebuild ] && sudo ln -sf $(CURDIR)/kukebuild $(INSTALL_PREFIX)/kukebuild || true
	@[ -f $(CURDIR)/kukepause ] && sudo ln -sf $(CURDIR)/kukepause $(INSTALL_PREFIX)/kukepause || true

uninstall-dev:
	sudo rm -f $(INSTALL_PREFIX)/kuke $(INSTALL_PREFIX)/kukeond $(INSTALL_PREFIX)/kukebuild $(INSTALL_PREFIX)/kukepause

# Publish the one-line installer through the mkdocs site: copy
# `scripts/install.sh` (canonical source) into `docs/site/install.sh` so
# `mkdocs build` picks it up and the GitHub Pages workflow serves it at
# https://kukeon.io/install.sh. A CI sync check (workflows/installer.yaml)
# runs the same `cp` against `--exit-code git diff` to catch drift.
.PHONY: install.sh
install.sh:
	cp scripts/install.sh docs/site/install.sh
	chmod +x docs/site/install.sh

# Built assets are committed so ordinary Go and Docker builds need no Node.js.
# Run explicitly after frontend edits; CI verifies the committed output.
.PHONY: web-ui web-ui-check
web-ui:
	cd internal/webui/frontend && npm ci --no-audit --no-fund && npm run build

web-ui-check: web-ui
	git diff --exit-code -- internal/webui/dist
	@test -z "$$(git ls-files --others --exclude-standard -- internal/webui/dist)" || \
		{ echo "Untracked generated web assets; regenerate and commit internal/webui/dist" >&2; exit 1; }

# Browser dependencies are installed by web-ui (or npm ci); install Chromium
# once with `cd internal/webui/frontend && npx playwright install chromium`.
.PHONY: web-ui-test web-ui-smoke
web-ui-test: kuke
	cd internal/webui/frontend && npm test

# Requires KUKE_WEB_SMOKE_HOST=unix:///path/to/a/running/kukeond.sock.
web-ui-smoke: kuke
	cd internal/webui/frontend && npm run test:smoke

# kuke team targets (harness checks, role materialization, live team checks).
# Optional so builds that copy only this Makefile (the Dockerfile) still parse.
-include team/team.mk
