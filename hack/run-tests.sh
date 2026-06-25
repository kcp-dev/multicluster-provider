#!/usr/bin/env bash

# Copyright 2025 The kcp Authors.
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

set -euo pipefail

cd $(dirname $0)/..
source hack/lib.sh

export KCP_ASSET_SHARDED_TEST_SERVER="$(UGET_PRINT_PATH=absolute make --no-print-directory install-sharded-test-server)"
export KCP_ASSET_KCP="$(UGET_PRINT_PATH=absolute make --no-print-directory install-kcp)"
export KCP_ASSET_KCP_FRONT_PROXY="$(UGET_PRINT_PATH=absolute make --no-print-directory install-kcp-front-proxy)"
export KCP_ASSET_CACHE_SERVER="$(UGET_PRINT_PATH=absolute make --no-print-directory install-cache-server)"
export CGO_ENABLED=0
export NO_GORUN=1
export KCP_TEST_WORK_DIR="$PWD"

go_test e2e -timeout 30m -v ./test/e2e/...
