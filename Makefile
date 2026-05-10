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

LDFLAGS := -s -w \
	-X $(MODULE)/cmd/config.Version=$(KUKEON_VERSION) \
	-X $(MODULE)/cmd/config.KukeondImageRepo=$(KUKEON_IMAGE_REPO)

# ----- Build matrix -----
BINS = kuke kukeond
OS = linux
ARCHS = amd64 arm64

# Where install-dev / uninstall-dev manage the host PATH symlinks. Contributors
# can override (e.g. INSTALL_PREFIX=$HOME/.local/bin) when /usr/local/bin is
# read-only or sudo-gated.
INSTALL_PREFIX ?= /usr/local/bin


all: clean kill $(BINS)

.PHONY: release
release: release-build

.PHONY: kuke kukeond
kuke:
	go build \
	-o kuke \
	-ldflags="$(LDFLAGS)" \
	./cmd/

# kukeond is the same binary as kuke, dispatched by argv[0] basename.
kukeond: kuke
	ln -sf kuke kukeond


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
		done \
	done

clean:
	rm -rf $(HOME)/.kukeon/run/*
	rm -rf kuke kukeond

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
test-e2e: kuke
	@echo "Building local kukeond image $(KUKEON_E2E_IMAGE_DOCKER_NAME) for e2e"
	docker build --build-arg VERSION=v0.0.0-e2e -t $(KUKEON_E2E_IMAGE_DOCKER_NAME) .
	@echo "Running e2e tests using binaries in project root"
	HOME=$(HOME) PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$(PATH) \
		E2E_BIN_DIR=$(CURDIR) \
		KUKEON_E2E_IMAGE=$(KUKEON_E2E_IMAGE) \
		KUKEON_E2E_IMAGE_DOCKER_NAME=$(KUKEON_E2E_IMAGE_DOCKER_NAME) \
		go test -v ./e2e

tag:
	git tag -s v$(KUKEON_VERSION) -m "Release version $(KUKEON_VERSION)"
	git push origin v$(KUKEON_VERSION)

.PHONY: dev-init
dev-init:
	./scripts/dev-init.sh

# Symlink the freshly built dev binaries onto a PATH directory so contributors
# can invoke `kuke` and `kukeond` directly after `make dev-init`. Symlink (not
# copy) so the next `make kuke` is picked up automatically — a hard copy would
# silently leave the previous build on PATH until the contributor remembers to
# re-install. argv[0] dispatch follows the basename of the path used to exec,
# so two parallel symlinks both pointing at the source binary work cleanly.
.PHONY: install-dev uninstall-dev
install-dev: kuke
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kuke
	sudo ln -sf $(CURDIR)/kuke $(INSTALL_PREFIX)/kukeond

uninstall-dev:
	sudo rm -f $(INSTALL_PREFIX)/kuke $(INSTALL_PREFIX)/kukeond
