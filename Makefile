# Copyright 2025 The KCP Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

export CGO_ENABLED ?= 0
export GOFLAGS ?= -mod=readonly -trimpath
export GO111MODULE = on
CMD ?= $(filter-out OWNERS, $(notdir $(wildcard ./cmd/*)))
ROOT_DIR=$(abspath .)
GOBUILDFLAGS ?= -v
GIT_HEAD ?= $(shell git log -1 --format=%H)
GIT_VERSION = $(shell git describe --tags --always)
BUILD_DEST ?= _build
GOTOOLFLAGS ?= $(GOBUILDFLAGS) -ldflags '-w $(LDFLAGS)' $(GOTOOLFLAGS_EXTRA)

export UGET_DIRECTORY = _tools
export UGET_CHECKSUMS = hack/tools.checksums
export UGET_VERSIONED_BINARIES = true

.PHONY: all
all: test

.PHONY: clean-tools
clean-tools:
	rm -rf $(UGET_DIRECTORY)
	@echo "Cleaned $(UGET_DIRECTORY)."

BOILERPLATE_VERSION ?= 0.3.0
GOLANGCI_LINT_VERSION ?= 2.8.0
KCP_VERSION ?= 0.30.0
WWHRD_VERSION ?= 06b99400ca6db678386ba5dc39bbbdcdadb664ff
YQ_VERSION ?= 4.44.6

.PHONY: install-boilerplate
install-boilerplate:
	@hack/uget.sh https://github.com/kubermatic-labs/boilerplate/releases/download/v{VERSION}/boilerplate_{VERSION}_{GOOS}_{GOARCH}.tar.gz boilerplate $(BOILERPLATE_VERSION)

.PHONY: install-golangci-lint
install-golangci-lint:
	@hack/uget.sh https://github.com/golangci/golangci-lint/releases/download/v{VERSION}/golangci-lint-{VERSION}-{GOOS}-{GOARCH}.tar.gz golangci-lint $(GOLANGCI_LINT_VERSION)

# wwhrd is installed as a Go module rather than from the provided
# binaries because there is no arm64 binary available from the author.
# See https://github.com/frapposelli/wwhrd/issues/141
.PHONY: install-wwhrd
install-wwhrd:
	@GO_MODULE=true hack/uget.sh github.com/frapposelli/wwhrd wwhrd $(WWHRD_VERSION)

.PHONY: install-kcp
install-kcp:
	@hack/uget.sh https://github.com/kcp-dev/kcp/releases/download/v{VERSION}/kcp_{VERSION}_{GOOS}_{GOARCH}.tar.gz kcp $(KCP_VERSION)

# This target can be used to conveniently update the checksums for all checksummed tools.
# Combine with GOARCH to update for other archs, like "GOARCH=arm64 make update-tools".

.PHONY: update-tools
update-tools: UGET_UPDATE=true
update-tools: clean-tools install-boilerplate install-golangci-lint install-kcp

GOLANGCI_LINT = $(ROOT_DIR)/$(UGET_DIRECTORY)/golangci-lint-$(GOLANGCI_LINT_VERSION)

.PHONY: lint
lint: install-golangci-lint
	@if [ -n "$(WHAT)" ]; then \
	  $(GOLANGCI_LINT) run --verbose -c $(ROOT_DIR)/.golangci.yaml $(WHAT); \
	else \
	  for MOD in . $$(git ls-files '**/go.mod' | sed 's,/go.mod,,'); do \
		(cd $$MOD; echo "Linting ./$$MOD ..."; $(GOLANGCI_LINT) run --verbose -c $(ROOT_DIR)/.golangci.yaml);\
	  done; \
	fi

.PHONY: fix-lint
fix-lint: install-golangci-lint
	GOLANGCI_LINT_FLAGS="--fix" $(MAKE) lint

.PHONY: imports
imports: WHAT ?=
imports: install-golangci-lint
	@if [ -n "$(WHAT)" ]; then \
	  $(GOLANGCI_LINT) fmt --enable gci -c $(ROOT_DIR)/.golangci.yaml $(WHAT); \
	else \
	  for MOD in . $$(git ls-files '**/go.mod' | sed 's,/go.mod,,'); do \
		(cd $$MOD; echo "Fixing ./$$MOD ..."; $(GOLANGCI_LINT) fmt --enable gci -c $(ROOT_DIR)/.golangci.yaml);\
	  done; \
	fi

.PHONY: verify
verify:
	./hack/verify-boilerplate.sh
	./hack/verify-licenses.sh
	for MOD in . $$(git ls-files '**/go.mod' | sed 's,/go.mod,,'); do \
		(cd $$MOD; echo "Tidying ./$$MOD ..."; go mod tidy);\
	done; \

.PHONY: test
test:
	./hack/run-tests.sh
