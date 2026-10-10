#!/usr/bin/env bash
# Runs every Go fuzz target for FUZZTIME each (default 20s).
#
#   FUZZTIME=1m scripts/fuzz.sh
#
# Targets are found by name (func FuzzXxx in *_test.go), so new ones are
# picked up automatically. A failing input is saved under the package's
# testdata/fuzz directory, which `go test` then replays as a regression test.
set -euo pipefail
cd "$(dirname "$0")/.."

FUZZTIME=${FUZZTIME:-20s}
found=0
while IFS= read -r file; do
  pkg="./$(dirname "$file")"
  for name in $(grep -oE '^func (Fuzz[A-Za-z0-9_]+)' "$file" | awk '{print $2}'); do
    found=$((found + 1))
    echo "== $pkg $name ($FUZZTIME)"
    # A short -fuzzminimizetime keeps progress visible: the default of 60s
    # per new input looks like a hang.
    go test "$pkg" -run '^$' -fuzz "^${name}\$" -fuzztime "$FUZZTIME" -fuzzminimizetime 2s
  done
done < <(grep -rl --include='*_test.go' -E '^func Fuzz' internal cmd | sort)

if [ "$found" -eq 0 ]; then
  echo "no fuzz targets found" >&2
  exit 1
fi
echo "ran $found fuzz targets"
