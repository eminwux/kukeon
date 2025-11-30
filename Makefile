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

# ----- Build matrix -----
BINS = kuke
OS = linux
ARCHS = amd64 arm64


all: clean kill $(BINS)

.PHONY: release
release: release-build

kuke:
	go build \
	-o kuke \
	-ldflags="-s -w -X $(MODULE)/cmd/config.Version=$(KUKEON_VERSION)" \
	./cmd/


release-build:
	# Build for all OS and ARCH combinations
	for OS in $(OS); do \
		for ARCH in $(ARCHS); do \
			GO111MODULE=on CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH \
			go build -a \
			-trimpath \
			-o kuke-$$OS-$$ARCH \
			-ldflags="-s -w -X $(MODULE)/cmd/config.Version=$(KUKEON_VERSION)" \
			./cmd; \
		done \
	done

clean:
	rm -rf $(HOME)/.kukeon/run/*
	rm -rf kuke

kill:
	(killall kukeond || true )

test:
	go test $(shell go list ./... | grep -v /e2e)

e2e: test-e2e
.PHONY: test-e2e
test-e2e:
	@echo "Running e2e tests using binaries in project root"
	HOME=$(HOME) E2E_BIN_DIR=$(CURDIR) go test -v ./e2e -v

tag:
	git tag -s v$(KUKEON_VERSION) -m "Release version $(KUKEON_VERSION)"
	git push origin v$(KUKEON_VERSION)
