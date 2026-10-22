# pi-openshell sandbox image — build, push, version.
#
# The image version of ghcr.io/dzungtr/pi-openshell is tracked in ./VERSION
# (moved here from the pi host repo ~/.pi so kape-io owns the release).
#
#   make build            # build :$(VERSION)-$(PROFILE) + rolling :$(PROFILE)
#   make push             # build + push both tags to ghcr
#   make bump-patch       # VERSION 0.1.1 -> 0.1.2 (also bump-minor/bump-major)
#
# Build context is THIS directory (openshell/agents) — run make from here.

RUNTIME  ?= podman
IMAGE    ?= ghcr.io/dzungtr/pi-openshell
PROFILE  ?= openrouter
VERSION  ?= $(shell cat VERSION 2>/dev/null || echo 0.0.0)

.PHONY: build push bump-patch bump-minor bump-major

build:
	@test -f VERSION || { echo "missing VERSION"; exit 1; }
	@test -f flake.nix || { echo "missing flake.nix"; exit 1; }
	@test -d profiles/$(PROFILE) || { echo "no profile: profiles/$(PROFILE)"; exit 1; }
	$(RUNTIME) build \
	  -t $(IMAGE):$(VERSION)-$(PROFILE) \
	  -t $(IMAGE):$(PROFILE) \
	  --build-arg PROFILE=$(PROFILE) \
	  .

push: build
	$(RUNTIME) push $(IMAGE):$(VERSION)-$(PROFILE)
	$(RUNTIME) push $(IMAGE):$(PROFILE)

bump-patch:
	awk -F. -v OFS=. '{ $$3++; print }' VERSION > VERSION.tmp && mv VERSION.tmp VERSION
	@echo "new version: $$(cat VERSION)"

bump-minor:
	awk -F. -v OFS=. '{ $$2++; $$3=0; print }' VERSION > VERSION.tmp && mv VERSION.tmp VERSION
	@echo "new version: $$(cat VERSION)"

bump-major:
	awk -F. -v OFS=. '{ $$1++; $$2=0; $$3=0; print }' VERSION > VERSION.tmp && mv VERSION.tmp VERSION
	@echo "new version: $$(cat VERSION)"
