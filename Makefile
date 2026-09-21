# Build Talos system extensions with bldr (as a BuildKit frontend, see the
# first line of Pkgfile). Modelled on siderolabs/extensions, trimmed to what
# this repo needs.

# TAG must be "vA.B.C[-N-gHASH][-dirty]": extensions-validator rejects any
# other shape. Fall back to v0.0.0-N-gHASH when the repo has no v-tag yet.
TAG ?= $(shell git describe --tags --dirty --match 'v[0-9]*' 2>/dev/null || echo "v0.0.0-$$(git rev-list --count HEAD 2>/dev/null || echo 0)-g$$(git rev-parse --short=8 HEAD 2>/dev/null || echo 00000000)$$(git diff --quiet 2>/dev/null || echo -dirty)")
SOURCE_DATE_EPOCH ?= $(shell git log -1 --pretty=%ct 2>/dev/null || date +%s)

REGISTRY ?= ghcr.io
USERNAME ?= audiobuzz
REGISTRY_AND_USERNAME ?= $(REGISTRY)/$(USERNAME)

# Talos base images. Must match the Talos release the extension targets:
# pkg/machinery/gendata/data/{pkgs,tools} in the Talos tree at that tag.
PKGS ?= v1.14.0-25-gf694e1b
PKGS_PREFIX ?= ghcr.io/siderolabs
TOOLS ?= v1.14.0-7-ga404efb
TOOLS_PREFIX ?= ghcr.io/siderolabs

PLATFORM ?= linux/amd64,linux/arm64
PROGRESS ?= auto
PUSH ?= false
ARTIFACTS := _out

BUILD := docker buildx build
BUILD_ARGS  = --build-arg=SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH)
BUILD_ARGS += --build-arg=TAG="$(TAG)"
BUILD_ARGS += --build-arg=PKGS="$(PKGS)"
BUILD_ARGS += --build-arg=PKGS_PREFIX="$(PKGS_PREFIX)"
BUILD_ARGS += --build-arg=TOOLS="$(TOOLS)"
BUILD_ARGS += --build-arg=TOOLS_PREFIX="$(TOOLS_PREFIX)"
COMMON_ARGS  = --file=Pkgfile
COMMON_ARGS += --provenance=false
COMMON_ARGS += --sbom=false
COMMON_ARGS += --progress=$(PROGRESS)
COMMON_ARGS += --platform=$(PLATFORM)
COMMON_ARGS += $(BUILD_ARGS)

TARGETS = wpa-supplicant

.PHONY: all $(TARGETS) clean help test

all: $(TARGETS)  ## Builds all extensions (result stays in the build cache).

target-%:  ## Builds one target from the Pkgfile.
	@$(BUILD) --target=$* $(COMMON_ARGS) $(TARGET_ARGS) .

local-%:  ## Builds one target and exports its filesystem to _out/<target>/.
	@$(MAKE) target-$* TARGET_ARGS="--output=type=local,dest=$(ARTIFACTS)/$* $(TARGET_ARGS)"

docker-%:  ## Builds one target as an image (use PUSH=true to push).
	@$(MAKE) target-$* TARGET_ARGS="$(TARGET_ARGS)"

# The image tag is the extension's VERSION (vars.yaml), e.g. 2.12-v0.1.0.
$(TARGETS):
	@$(MAKE) docker-$@ TARGET_ARGS="--tag=$(REGISTRY_AND_USERNAME)/$@:$(shell $(MAKE) -s version-$@) --push=$(PUSH) $(TARGET_ARGS)"

version-%:  ## Prints the version bldr would assign to a target.
	@docker run --rm --user $(shell id -u):$(shell id -g) --volume $(PWD):/src --entrypoint=/bldr \
	  ghcr.io/siderolabs/bldr:v0.6.3 --root=/src eval --target $* --build-arg TAG=$(TAG) '{{.VERSION}}' 2>/dev/null

test:  ## Unit tests of the Go entrypoint.
	cd network/wpa-supplicant/boot && go vet ./... && go test ./...

clean:
	rm -rf $(ARTIFACTS)

help:  ## This help.
	@grep -E '^[a-zA-Z_%-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'
