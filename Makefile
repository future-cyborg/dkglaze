# Support the "list" target below. Must appear before any `include` directives.
THIS_FILE := $(lastword $(MAKEFILE_LIST))

# Usage:
#   make list                             list all available targets
#
#   make bin                              all bin/ targets
#   make bin/$(PLATFORM)                  all bin/$(PLATFORM)/ targets
#   make bin/$(PLATFORM)/$(BUILD)
#
#   make docker                           all docker/ targets
#   make docker/$(PLATFORM)               all docker/$(PLATFORM)/ targets
#   make docker/$(PLATFORM)/$(BUILD)
#
#   make bench-test                       run bench tests
#   make bench-test/$(GO_PKG)             run bench tests on single package
#   make unit-test                        run unit tests
#   make unit-test/$(GO_PKG)              run unit tests on single package
#   make unit-test-cover                  open coverage heatmap in browser
#   make unit-test-cover/$(GO_PKG)        open coverage heatmap in browser, single package
#
#   make go/generate                      apply go generators
#   make go/format                        apply lint fixers
#   make go/lint                          runs linters
#   make go/tidy                          update go.mod from source code
#
#   make print-BUILDS                     print available build targets
#   make print-PLATFORMS                  print supported platforms
#
#   make clean                            removes bin/ report/
#
#   make tag/bump-minor-and-push          tag minor version and push
#   make tag/bump-patch-and-push          tag patch version and push

SHELL              = /bin/bash -o pipefail

MAKEFILE_PATH      := $(abspath $(lastword $(MAKEFILE_LIST)))
MAKEFILE_DIR       := $(dir $(MAKEFILE_PATH))
# NOTE: include other makefiles only after this line

DEFAULT_BRANCH     := main
PROJECT_NAME       := udocker
GIT_REPO           := git@github.com:future-cyborg/udocker.git
REPO_NAME          := github.com/future-cyborg/udocker

GIT_COMMIT         := $(shell git rev-parse --short HEAD)
GIT_DESCRIBE       := $(shell git describe --match 'v[0-9]*.[0-9]*.[0-9]*' --dirty)
SEMVER             := $(patsubst v%,%, $(GIT_DESCRIBE))
SEMVER_MINOR_BUMP  := $(shell echo '$(GIT_DESCRIBE)' | sed 's/-.*$$//;s/[.]/ /g' | awk '{ print $$1 "." $$2+1 ".0" }')
SEMVER_PATCH_BUMP  := $(shell echo '$(GIT_DESCRIBE)' | sed 's/-.*$$//;s/[.]/ /g' | awk '{ print $$1 "." $$2 "." $$3+1 }')

BASE_IMAGE         := golang:1.26-bookworm
LINT_IMAGE         := golangci/golangci-lint:v2.12.1-alpine

BUILD_ARGS         := --build-arg BUILDKIT_INLINE_CACHE=1
BUILD_ARGS         += --build-arg BASE_IMAGE=$(BASE_IMAGE)
BUILD_ARGS         += --build-arg LINT_IMAGE=$(LINT_IMAGE)

BUILD_FLAGS        := -v

# BUILDS is populated with the name of each directory in cmd/
BUILDS             := $(foreach P,$(patsubst %/,%,$(dir $(wildcard $(MAKEFILE_DIR)cmd/*/.))),$(notdir $P))
# GO_PKGS is populated with the name of each directory in internal/
GO_PKGS            := $(foreach P,$(patsubst %/,%,$(dir $(wildcard $(MAKEFILE_DIR)internal/*/.))),$(notdir $P))

# PLATFORMS lists all supported platforms - MUST NOT CONTAIN DASHES (-)
PLATFORMS          := linux/x86_64 linux/arm64
LOCAL_PLATFORM     := $(shell uname -s | tr '[:upper:]' '[:lower:]')/$(shell uname -m | sed 's/aarch64/arm64/')

noop=
space = $(noop) $(noop)

# Check that given variables are set and all have non-empty values,
# die with an error otherwise.
#
# Params:
#   1. Variable name(s) to test.
#   2. (optional) Error message to print.
check_defined = $(strip $(foreach 1,$1,$(call __check_defined,$1,"$1 is undefined: $(strip $(value 2))")))
__check_defined = $(if $(value $1),,$(error Undefined $1$(if $2, ($2))))

$(call check_defined, BASE_IMAGE)
$(call check_defined, BUILD_ARGS)
$(call check_defined, DEFAULT_BRANCH)
$(call check_defined, GIT_COMMIT)
$(call check_defined, GIT_DESCRIBE)
$(call check_defined, GIT_REPO)
$(call check_defined, LINT_IMAGE)
$(call check_defined, MAKEFILE_PATH)
$(call check_defined, MAKEFILE_DIR)
$(call check_defined, PROJECT_NAME)
$(call check_defined, REPO_NAME)
$(call check_defined, SEMVER)
$(call check_defined, SEMVER_MINOR_BUMP)
$(call check_defined, SEMVER_PATCH_BUMP)

.DEFAULT_GOAL      := list

# Make sure we don't use parallel Make jobs. Neither Docker nor Docker Compose appreciates it.
.NOTPARALLEL:

# Allow `make list` to list all targets.
# https://stackoverflow.com/questions/4219255/how-do-you-get-the-list-of-targets-in-a-makefile
.PHONY: list
list:
	@$(MAKE) -pRrq -f $(THIS_FILE) : 2>/dev/null | awk -F: '/^# Files/,/^# Finished Make data base/ {if ($$1 !~ "^[#. ]" && $$1 != "") {print $$1}}' | sort -u | grep -E -v -e '^[^[:alnum:]]' -e '^$@$$' -e '^Makefile$$' || true

print-%: ; $(info $* is a $(flavor $*) variable set to [$($*)]) @true

.ensure-dir-%:
	mkdir -p "$(MAKEFILE_DIR)$*"

.PHONY: clean
clean:
	rm -rf $(MAKEFILE_DIR)bin $(MAKEFILE_DIR)report

####
# Go
####

.PHONY: go/tidy
go/tidy:
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--output . \
		--progress plain \
		--target run-tidy \
		.

.PHONY: go/format
go/format:
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg REPO_NAME=$(REPO_NAME) \
		--output . \
		--progress plain \
		--target run-format \
		.

.PHONY: go/generate
go/generate:
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--output . \
		--progress plain \
		--target run-generate \
		.

.PHONY: go/lint
go/lint: .ensure-dir-report
	rm -f $(MAKEFILE_DIR)report/lint-run* 2>/dev/null
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--output report/ \
		--progress plain \
		--target run-linters \
		.
	cat $(MAKEFILE_DIR)report/lint-run
	@[ "$$(cat $(MAKEFILE_DIR)report/lint-run.exitcode)" = "0" ] || exit 1

######
# test
######

.PHONY: unit-test
unit-test: .ensure-dir-report
	rm -f $(MAKEFILE_DIR)report/unit-test-run* 2>/dev/null
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg TEST_PATH="$(REPO_NAME)/internal/..." \
		--build-arg TEST_FLAGS=$(TEST_FLAGS) \
		--output report/ \
		--progress plain \
		--target run-unit-tests \
		.
	cat $(MAKEFILE_DIR)report/unit-test-run
	@[ "$$(cat $(MAKEFILE_DIR)report/unit-test-run.exitcode)" = "0" ] || exit 1

.PHONY: $(GO_PKGS:%=unit-test/%)
$(GO_PKGS:%=unit-test/%): unit-test/%: .ensure-dir-report
	rm -f $(MAKEFILE_DIR)report/unit-test-run* 2>/dev/null
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg TEST_PATH="$(REPO_NAME)/internal/$*" \
		--build-arg TEST_FLAGS=$(TEST_FLAGS) \
		--output report/ \
		--progress plain \
		--target run-unit-tests \
		.
	cat $(MAKEFILE_DIR)report/unit-test-run
	@[ "$$(cat $(MAKEFILE_DIR)report/unit-test-run.exitcode)" = "0" ] || exit 1

.PHONY: unit-test-cover
unit-test-cover: unit-test
	go tool cover -html=$(MAKEFILE_DIR)report/unit-test-run.cover.out

.PHONY: $(GO_PKGS:%=unit-test-cover/%)
$(GO_PKGS:%=unit-test-cover/%): unit-test-cover/%: unit-test/%
	go tool cover -html=$(MAKEFILE_DIR)report/unit-test-run.cover.out

.PHONY: bench-test
bench-test: .ensure-dir-report
	rm -f $(MAKEFILE_DIR)report/bench-test-run* 2>/dev/null
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg TEST_PATH="$(REPO_NAME)/internal/..." \
		--build-arg TEST_FLAGS=$(TEST_FLAGS) \
		--output report/ \
		--progress plain \
		--target run-bench-tests \
		.
	cat $(MAKEFILE_DIR)report/bench-test-run
	@[ "$$(cat $(MAKEFILE_DIR)report/bench-test-run.exitcode)" = "0" ] || exit 1

.PHONY: $(GO_PKGS:%=bench-test/%)
$(GO_PKGS:%=bench-test/%): bench-test/%: .ensure-dir-report
	rm -f $(MAKEFILE_DIR)report/bench-test-run* 2>/dev/null
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg TEST_PATH="$(REPO_NAME)/internal/$*" \
		--build-arg TEST_FLAGS=$(TEST_FLAGS) \
		--output report/ \
		--progress plain \
		--target run-bench-tests \
		.
	cat $(MAKEFILE_DIR)report/bench-test-run
	@[ "$$(cat $(MAKEFILE_DIR)report/bench-test-run.exitcode)" = "0" ] || exit 1

######
# bin/
######

.PHONY: bin
bin: $(PLATFORMS:%=bin/%)

.PHONY: docker
docker: $(PLATFORMS:%=docker/%)

define makeBin
PLATFORM_BUILD_TARGETS=$(patsubst %, bin/$1/%,$(BUILDS))

.PHONY: bin/$1
bin/$1: $(PLATFORM_BUILD_TARGETS)

.PHONY: $(BUILDS:%=bin/$1/%) # phony since we don't have the dependencies to the source files
$(BUILDS:%=bin/$1/%): bin/$1/%:
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg BUILD=$$* \
		--build-arg BUILD_FLAGS="$(BUILD_FLAGS)" \
		--output bin/$1 \
		--platform $1 \
		--progress plain \
		--target build-bin \
		.

endef

$(foreach name,$(PLATFORMS),$(eval $(call makeBin,$(name))))

####################
# Docker - Go builds
####################

define makeDockerBuild
PLATFORM_DOCKER_TARGETS=$(patsubst %, docker/$1/%,$(BUILDS))

.PHONY: docker/$1
docker/$1: $(PLATFORM_DOCKER_TARGETS)

.PHONY: $(BUILDS:%=docker/$1/%)
$(BUILDS:%=docker/$1/%): docker/$1/%:
	cd $(MAKEFILE_DIR) && DOCKER_BUILDKIT=1 docker build \
		$(BUILD_ARGS) \
		--build-arg BUILD=$$* \
		--build-arg BUILDKIT_INLINE_CACHE=1 \
		--build-arg BUILD_FLAGS="$(BUILD_FLAGS)" \
		--label "com.repo.git.commit=$(GIT_COMMIT)" \
		--label "com.repo.git.describe=$(GIT_DESCRIBE)" \
		--label "com.repo.git.repo=$(GIT_REPO)" \
		--platform $1 \
		--progress plain \
		--tag "$(PROJECT_NAME)/$$*:$(SEMVER)-$(subst /,-,$1)" \
		--target build-docker-image \
		.

endef

$(foreach name,$(PLATFORMS),$(eval $(call makeDockerBuild,$(name))))

######################
# Docker - Dockerfiles
######################

# Find Docker images that can be built from this repo. These come from Dockerfiles nested under the build/docker directory
DOCKERFILES:=$(wildcard build/docker/*/Dockerfile)
DOCKERFILE_IMAGE_NAMES=$(patsubst build/docker/%/Dockerfile,%,$(DOCKERFILES))

define makeDockerfiles

$1_DEPENDS=$(patsubst %, bin/%,$($1_BIN))

.PHONY: docker/$1
docker/$1: $$($1_DEPENDS)
	docker build \
		--build-arg SEMVER="$(SEMVER)" \
		--label "com.repo.git.commit=$(GIT_COMMIT)" \
		--label "com.repo.git.describe=$(GIT_DESCRIBE)" \
		--label "com.repo.git.repo=$(GIT_REPO)" \
		--progress plain \
		--tag "$(PROJECT_NAME)/$(subst /,-,$1):$(SEMVER)" \
		build/docker/$1

endef

$(foreach name,$(DOCKERFILE_IMAGE_NAMES),$(eval $(call makeDockerfiles,$(name))))

#########
# Tagging
#########

.PHONY: .enforce-updated-branch
.enforce-updated-branch:
	@echo Enforcing branch is up to date
	@git remote update
	@[[ "$$(git rev-parse --short HEAD)" == "$$(git rev-parse --short origin/$(DEFAULT_BRANCH))" ]]

.PHONY: .enforce-default-branch
.enforce-default-branch:
	@echo Enforcing default branch: $(DEFAULT_BRANCH)
	@[[ "$$(git rev-parse --symbolic-full-name --abbrev-ref HEAD)" == $(DEFAULT_BRANCH) ]]

.PHONY: tag/bump-minor-and-push
tag/bump-minor-and-push: .enforce-default-branch .enforce-updated-branch
	git tag -a "${SEMVER_MINOR_BUMP}" -m "${SEMVER_MINOR_BUMP}"
	git push origin "${SEMVER_MINOR_BUMP}"

.PHONY: tag/bump-patch-and-push
tag/bump-patch-and-push: .enforce-default-branch .enforce-updated-branch
	git tag -a "${SEMVER_PATCH_BUMP}" -m "${SEMVER_PATCH_BUMP}"
	git push origin "${SEMVER_PATCH_BUMP}"


#################
# build & install
#################

.PHONY: build
build: bin/$(LOCAL_PLATFORM)/udocker

.PHONY: install
install:
	cp $(MAKEFILE_DIR)bin/$(LOCAL_PLATFORM)/udocker $(HOME)/.local/bin/udocker

