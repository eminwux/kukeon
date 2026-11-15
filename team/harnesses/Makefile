# Base dependencies are coordinated here; each harness owns its entrypoint.
REGISTRY ?= ghcr.io/eminwux/kuketeams
TAG ?= latest
MODE ?= load
PLATFORMS ?= linux/$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IMAGES ?=
export REGISTRY TAG MODE PLATFORMS
# Aggregate subsets may omit a child harness or the explicit base build.
export ALLOW_EMPTY_SUBSET := 1

.PHONY: images bases plan check smoke
images: bases
	+$(MAKE) -C claude images IMAGES="$(IMAGES)"
	+$(MAKE) -C codex images IMAGES="$(IMAGES)"
	+$(MAKE) -C opencode images IMAGES="$(IMAGES)"

bases:
	@HARNESS_PATH=base ./build-images.sh $(IMAGES)

plan:
	@MODE=check ./build-images.sh --print $(IMAGES)

check:
	$(MAKE) -C .. test-harnesses

smoke:
	python3 tests/smoke_images.py
