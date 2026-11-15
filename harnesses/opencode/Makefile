# Build contexts and Dockerfiles come exclusively from this Harness document.
REGISTRY ?= ghcr.io/eminwux/kuketeams
TAG ?= latest
MODE ?= load
PLATFORMS ?= linux/$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
IMAGES ?=
export REGISTRY TAG MODE PLATFORMS

.PHONY: images plan
images:
	@HARNESS_PATH="harnesses/$(notdir $(CURDIR))" ../build-images.sh $(IMAGES)

plan:
	@HARNESS_PATH="harnesses/$(notdir $(CURDIR))" MODE=check ../build-images.sh --print $(IMAGES)
