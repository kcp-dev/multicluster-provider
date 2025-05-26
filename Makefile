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
GOBUILDFLAGS ?= -v
GIT_HEAD ?= $(shell git log -1 --format=%H)
GIT_VERSION = $(shell git describe --tags --always)
BUILD_DEST ?= _build
GOTOOLFLAGS ?= $(GOBUILDFLAGS) -ldflags '-w $(LDFLAGS)' $(GOTOOLFLAGS_EXTRA)
GOARCH ?= $(shell go env GOARCH)
GOOS ?= $(shell go env GOOS)

.PHONY: all
all: test

GOLANGCI_LINT = _tools/golangci-lint
GOLANGCI_LINT_VERSION = 1.64.2

.PHONY: $(GOLANGCI_LINT)
$(GOLANGCI_LINT):
	@hack/download-tool.sh \
	  https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-${GOOS}-${GOARCH}.tar.gz \
	  golangci-lint \
	  ${GOLANGCI_LINT_VERSION}

# wwhrd is installed as a Go module rather than from the provided
# binaries because there is no arm64 binary available from the author.
# See https://github.com/frapposelli/wwhrd/issues/141

WWHRD = _tools/wwhrd
WWHRD_VERSION = 06b99400ca6db678386ba5dc39bbbdcdadb664ff

.PHONY: $(WWHRD)
$(WWHRD):
	@GO_MODULE=true hack/download-tool.sh \
	  github.com/frapposelli/wwhrd \
	  wwhrd \
	  ${WWHRD_VERSION}

BOILERPLATE = _tools/boilerplate
BOILERPLATE_VERSION = 0.3.0

.PHONY: $(BOILERPLATE)
$(BOILERPLATE):
	@hack/download-tool.sh \
	  https://github.com/kubermatic-labs/boilerplate/releases/download/v${BOILERPLATE_VERSION}/boilerplate_${BOILERPLATE_VERSION}_${GOOS}_${GOARCH}.tar.gz \
	  boilerplate \
	  ${BOILERPLATE_VERSION}

YQ = _tools/yq
YQ_VERSION = 4.44.6

.PHONY: $(YQ)
$(YQ):
	@UNCOMPRESSED=true hack/download-tool.sh \
	  https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_${GOOS}_${GOARCH} \
	  yq \
	  ${YQ_VERSION} \
	  yq_*

KCP = _tools/kcp
KCP_VERSION = 0.26.1

.PHONY: $(KCP)
$(KCP):
	@hack/download-tool.sh \
	  https://github.com/kcp-dev/kcp/releases/download/v${KCP_VERSION}/kcp_${KCP_VERSION}_${GOOS}_${GOARCH}.tar.gz \
	  kcp \
	  ${KCP_VERSION}

ENVTEST = _tools/setup-envtest
ENVTEST_VERSION = release-0.19

.PHONY: lint
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run \
		--verbose \
		--print-resources-usage \
		./...

.PHONY: imports
imports: WHAT ?=
imports: $(GOLANGCI_LINT)
	@if [ -n "$(WHAT)" ]; then \
	  $(GOLANGCI_LINT) run --enable-only=gci --fix --fast $(WHAT); \
	else \
	  for MOD in . $$(git ls-files '**/go.mod' | sed 's,/go.mod,,'); do \
		(cd $$MOD; $(GOLANGCI_LINT) run --enable-only=gci --fix --fast); \
	  done; \
	fi

.PHONY: verify
verify:
	./hack/verify-boilerplate.sh
	./hack/verify-licenses.sh

.PHONY: test
test: $(KCP)
	./hack/run-tests.sh
