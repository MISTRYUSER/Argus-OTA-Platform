#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
stage=${1:-regression}
go_bin=${GO_BIN:-go}
mkdir -p .regression-results
"$go_bin" test -count=1 -json ./... > ".regression-results/$stage-tests.json"
"$go_bin" vet ./... > ".regression-results/$stage-vet.txt" 2>&1
"$go_bin" build ./... > ".regression-results/$stage-build.txt" 2>&1
printf '%s: full tests, vet and build passed\n' "$stage"
