#!/usr/bin/env bash

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

set -euo pipefail

cd $(dirname $0)/..
source hack/lib.sh

BOILERPLATE="$(UGET_PRINT_PATH=relative make --no-print-directory install-boilerplate)"

echo "Checking file boilerplates…"

"$BOILERPLATE" \
  -boilerplates hack/boilerplate \
  -exclude .github \
  -exclude internal/cache/forked_cache_reader.go \
  -exclude internal/events/recorder/forked_recorder.go \
  -exclude envtest \
  -exclude hack/uget.sh

"$BOILERPLATE" \
  -boilerplates hack/boilerplate/kubernetes \
  -exclude envtest/doc.go \
  -exclude envtest/eventually.go \
  -exclude envtest/scheme.go \
  -exclude envtest/testing.go \
  -exclude envtest/workspaces.go \
  internal/cache/forked_cache_reader.go \
  internal/events/recorder/forked_recorder.go

"$BOILERPLATE" \
  -boilerplates hack/boilerplate \
  envtest/doc.go \
  envtest/eventually.go \
  envtest/scheme.go \
  envtest/testing.go \
  envtest/workspaces.go
