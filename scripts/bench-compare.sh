#!/usr/bin/env bash
# Compares benchmarks of the working tree with a base revision.
#
#   scripts/bench-compare.sh origin/main
#
# Prints benchstat's table. Packages that don't exist at the base show only
# the new numbers. BENCH_COUNT sets the number of runs (default 6).
set -euo pipefail
cd "$(dirname "$0")/.."

base=${1:?usage: scripts/bench-compare.sh BASE_REF}
count=${BENCH_COUNT:-6}
tmp=$(mktemp -d)
cleanup() {
  git worktree remove --force "$tmp/base" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

run() {
  # Keep only benchmark lines and their headers; benchstat ignores the rest.
  go test -run '^$' -bench . -benchmem -count "$count" ./... 2>/dev/null |
    grep -E '^(Benchmark|goos:|goarch:|pkg:|cpu:)' || true
}

echo "benchmarking working tree..." >&2
run >"$tmp/new.txt"
echo "benchmarking $base..." >&2
git worktree add --detach "$tmp/base" "$base" >/dev/null 2>&1
(cd "$tmp/base" && run) >"$tmp/old.txt"

# Relative names become the column headers.
benchstat=golang.org/x/perf/cmd/benchstat@latest
(cd "$tmp" && go run "$benchstat" old.txt new.txt)
