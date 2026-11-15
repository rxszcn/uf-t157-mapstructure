#!/usr/bin/env bash
#
# Width-ladder benchmark for the map-key fallback matching path.
#
# Usage:
#   scripts/bench-wide.sh              # default: 5 iterations per sub-benchmark
#   BENCHTIME=10x scripts/bench-wide.sh
#   BENCH='Benchmark_DecodeWide$' scripts/bench-wide.sh   # default+fold+miss only
#
# The benchmark itself covers widths 250/500/1000/2000/4000 (increasing), for
# three key shapes: exact (direct lookup), fold (case-only mismatch, fallback
# matching), and miss (no key matches, worst case for the fallback scan).

set -euo pipefail
cd "$(dirname "$0")/.."

BENCHTIME="${BENCHTIME:-5x}"
BENCH="${BENCH:-Benchmark_DecodeWide}"

go test -run '^$' -bench "$BENCH" -benchtime "$BENCHTIME" -count 1
