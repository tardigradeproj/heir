IMAGE_REGISTRY ?= ghcr.io/tardigrade
IMAGE_NAME     ?= samaritano-base
IMAGE_TAG      ?= latest

.PHONY: build-base
build-base:
	docker build \
		-f images/base/Dockerfile \
		-t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) \
		.
