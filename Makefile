RELEASE_DIR := release

# ----- Version sourcing -----
MODULE := $(shell go list -m)

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
BINS = kuke kukeond kuketty kukebuild
OS = linux
ARCHS = amd64 arm64


all: clean kill $(BINS)

.PHONY: release
release: release-build

.PHONY: kuke kukeond kuketty kukebuild
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
kuketty:
	go build \
	-o kuketty \
	-ldflags="$(LDFLAGS)" \
	./cmd/kuketty/

# kukebuild is the native image builder that embeds BuildKit as a library
# (issue #522). Like kuketty it is a separate binary (not argv[0]-dispatched
# from kuke) so BuildKit's transitive moby / runc / grpc closure stays out of
# the kuke + kukeond import sets. `kuke build` shells out to it on PATH.
kukebuild:
	go build \
	-o kukebuild \
	-ldflags="$(LDFLAGS)" \
	./cmd/kukebuild/


release-build:
	# Build for all OS and ARCH combinations
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
			-o kukebuild-$$OS-$$ARCH \
			-ldflags="$(LDFLAGS)" \
			./cmd/kukebuild; \
		done \
	done

clean:
	rm -rf $(HOME)/.kukeon/run/*
	rm -rf kuke kukeond kuketty kukebuild

kill:
	(killall kukeond || true )

test:
	go test $(shell go list ./... | grep -v /e2e)

# Tag the e2e suite uses to refer to the local kukeond image. The e2e harness
# imports it into containerd's kuke-system namespace before running `kuke init`,
# so the test does not depend on a published registry tag matching `git
# describe`.
KUKEON_E2E_IMAGE_DOCKER_NAME ?= kukeon-local:e2e
KUKEON_E2E_IMAGE ?= docker.io/library/$(KUKEON_E2E_IMAGE_DOCKER_NAME)

e2e: test-e2e
.PHONY: test-e2e
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
# `KUKE_INIT_SERVER_CONFIGURATION=/dev/null` short-circuits cross-test
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
test-e2e: kuke kukeond kuketty
	@echo "Building local kukeond image $(KUKEON_E2E_IMAGE_DOCKER_NAME) for e2e"
	docker build --build-arg VERSION=v0.0.0-e2e -t $(KUKEON_E2E_IMAGE_DOCKER_NAME) .
	@echo "Running e2e tests using binaries in project root"
	@if [ -x /.kukeon/bin/kuketty ]; then \
		echo "[nested mode: kuke init --run-path <tempdir> derives <tempdir>/kukeond.sock; parent dev cell's /run/kukeon/ socket untouched]"; \
	fi
	HOME=$(HOME) PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$(PATH) \
		E2E_BIN_DIR=$(CURDIR) \
		KUKEON_E2E_IMAGE=$(KUKEON_E2E_IMAGE) \
		KUKEON_E2E_IMAGE_DOCKER_NAME=$(KUKEON_E2E_IMAGE_DOCKER_NAME) \
		KUKE_INIT_SERVER_CONFIGURATION=/dev/null \
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
.PHONY: install-dev uninstall-dev
install-dev: kuke
	ln -sf kuke kukeond
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kuke
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kukeond

uninstall-dev:
	sudo rm -f $(INSTALL_PREFIX)/kuke $(INSTALL_PREFIX)/kukeond

# Publish the one-line installer through the mkdocs site: copy
# `scripts/install.sh` (canonical source) into `docs/site/install.sh` so
# `mkdocs build` picks it up and the GitHub Pages workflow serves it at
# https://kukeon.io/install.sh. A CI sync check (workflows/installer.yaml)
# runs the same `cp` against `--exit-code git diff` to catch drift.
.PHONY: install.sh
install.sh:
	cp scripts/install.sh docs/site/install.sh
	chmod +x docs/site/install.sh
