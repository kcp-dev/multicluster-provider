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

### Contains commonly used functions for the other scripts.

# Required for signal propagation to work so
# the cleanup trap gets executed when a script
# receives a SIGINT
set -o monitor

retry() {
  # Works only with bash but doesn't fail on other shells
  start_time=$(date +%s)
  set +e
  # We use an extra wrapping to write junit and have a timer
  retry_backoff $@
  rc=$?
  set -e
  elapsed_time=$(($(date +%s) - $start_time))
  write_junit "$rc" "$elapsed_time"
  return $rc
}

retry_backoff() {
  retries=$1
  shift

  count=0
  delay=1
  until "$@"; do
    rc=$?
    count=$((count + 1))
    if [ $count -lt "$retries" ]; then
      echo "Retry $count/$retries exited $rc, retrying in $delay seconds…" > /dev/stderr
      sleep $delay
    else
      echo "Retry $count/$retries exited $rc, no more retries left." > /dev/stderr
      return $rc
    fi
    delay=$((delay * 2))
  done
  return 0
}

retry_linear() {
  delay=$1
  retries=$2
  shift
  shift

  count=0
  until "$@"; do
    rc=$?
    count=$((count + 1))
    if [ $count -lt "$retries" ]; then
      echodate "[$count/$retries] Command returned $rc, retrying…"
      sleep $delay
    else
      echodate "Command returned $rc, no more retries left."
      return $rc
    fi
  done

  echodate "Command succeeded."

  return 0
}

echodate() {
  # do not use -Is to keep this compatible with macOS
  echo "[$(date +%Y-%m-%dT%H:%M:%S%:z)]" "$@" > /dev/stderr
}

write_junit() {
  # Doesn't make any sense if we don't know a testname
  if [ -z "${TEST_NAME:-}" ]; then return; fi
  # Only run in CI
  if [ -z "${ARTIFACTS:-}" ]; then return; fi

  rc=$1
  duration=${2:-0}
  errors=0
  failure=""
  if [ "$rc" -ne 0 ]; then
    errors=1
    failure='<failure type="Failure">Step failed</failure>'
  fi
  TEST_CLASS="${TEST_CLASS:-Kubermatic}"
  cat << EOF > ${ARTIFACTS}/junit.$(echo $TEST_NAME | sed 's/ /_/g' | tr '[:upper:]' '[:lower:]').xml
<?xml version="1.0" ?>
<testsuites>
  <testsuite errors="$errors" failures="$errors" name="$TEST_CLASS" tests="1">
    <testcase classname="$TEST_CLASS" name="$TEST_NAME" time="$duration">
      $failure
    </testcase>
  </testsuite>
</testsuites>
EOF
}

repeat() {
  local end=$1
  local str="${2:-=}"

  for i in $(seq 1 $end); do
    echo -n "${str}"
  done
}

heading() {
  local title="$@"
  echo "$title"
  repeat ${#title} "="
  echo
}

# go_test wraps running `go test` commands. The first argument needs to be file name
# for a junit result file that will be generated if go-junit-report is present and
# $ARTIFACTS is set. The remaining arguments are passed to `go test`.
go_test() {
  local junit_name="${1:-}"
  shift

  # only run go-junit-report if binary is present and we're in CI / the ARTIFACTS environment is set
  if [ -x "$(command -v go-junit-report)" ] && [ ! -z "${ARTIFACTS:-}" ]; then
    go test "$@" 2>&1 | go-junit-report -set-exit-code -iocopy -out ${ARTIFACTS}/junit.${junit_name}.xml
  else
    go test "$@"
  fi
}
