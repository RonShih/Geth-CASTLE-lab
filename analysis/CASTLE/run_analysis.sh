#!/bin/bash
# run_analysis.sh — Trie-traversal optrace analysis (2-stage Go pipeline)
#
#   Stage 1 (code_optrace_analysis/analysis_optrace.go):
#       optrace_<range>_<ts>            -> trie_traversal_optrace.csv
#   Stage 2 (code_trie_traversal_optrace_analysis/analysis_trie_traversal_optrace.go):
#       trie_traversal_optrace.csv      -> read_ops_analyzed.csv
#
# Plotting / report generation is intentionally NOT done here yet.
#
# Usage:
#   ./run_analysis.sh                    # analyze latest optrace_* in $EXEC_DIR
#   ./run_analysis.sh 11350800_11351000  # analyze a specific block range
#
# Env overrides:
#   EXEC_DIR         where to look for optrace_* input  (default: ~/ethereum/execution)
#   OUTPUT_BASE_DIR  parent of raOutput_<range>/        (default: alongside this script)

set -e

EXEC_DIR="${EXEC_DIR:-$HOME/ethereum/execution}"
ANALYSIS_DIR="$(cd "$(dirname "$0")" && pwd)"
PARENT_DIR="$(dirname "$ANALYSIS_DIR")"
cd "$ANALYSIS_DIR"

BLOCK_RANGE="$1"

# ── Binaries + sources ───────────────────────────────────────────────────────
STAGE1_BIN="$PARENT_DIR/bin/analysisReadAmplification"
STAGE2_BIN="$PARENT_DIR/bin/analysisTrieTraversalOptrace"
STAGE1_SRC="$ANALYSIS_DIR/code_optrace_analysis/analysis_optrace.go"
STAGE2_SRC="$ANALYSIS_DIR/code_trie_traversal_optrace_analysis/analysis_trie_traversal_optrace.go"

# Build (or rebuild) if a binary is missing or its source is newer.
need_build=0
[ ! -f "$STAGE1_BIN" ] && need_build=1
[ ! -f "$STAGE2_BIN" ] && need_build=1
[ -f "$STAGE1_BIN" ] && [ "$STAGE1_SRC" -nt "$STAGE1_BIN" ] && need_build=1
[ -f "$STAGE2_BIN" ] && [ "$STAGE2_SRC" -nt "$STAGE2_BIN" ] && need_build=1
if [ "$need_build" -eq 1 ]; then
  echo "Building analysis binaries via build.sh ..."
  bash "$PARENT_DIR/build.sh" build || echo "  WARNING: build.sh reported errors."
fi
for b in "$STAGE1_BIN" "$STAGE2_BIN"; do
  if [ ! -x "$b" ]; then
    echo "Error: required binary not found: $b"
    echo "  First-time setup: (cd $PARENT_DIR && bash build.sh install && bash build.sh build)"
    exit 1
  fi
done

# ── Resolve input optrace file ───────────────────────────────────────────────
if [ -n "$BLOCK_RANGE" ]; then
  TRACE_FILE=$(ls -t "$EXEC_DIR/optrace_${BLOCK_RANGE}"* 2>/dev/null | head -1)
else
  TRACE_FILE=$(ls -t "$EXEC_DIR/optrace_"* 2>/dev/null | head -1)
fi
if [ -z "$TRACE_FILE" ]; then
  echo "Error: no optrace_* file found in $EXEC_DIR"
  exit 1
fi

RANGE=$(basename "$TRACE_FILE" | sed "s/^optrace_\([0-9]*_[0-9]*\).*/\1/")
OUTPUT_DIR="./raOutput_${RANGE}"
if [ -n "${OUTPUT_BASE_DIR}" ]; then
  OUTPUT_DIR="${OUTPUT_BASE_DIR}/raOutput_${RANGE}"
fi
RA_DIR="$OUTPUT_DIR/optrace_analysis"
mkdir -p "$RA_DIR"

TRAV_CSV="$RA_DIR/trie_traversal_optrace.csv"
READ_OPS_CSV="$RA_DIR/read_ops_analyzed.csv"

echo "Input:  $TRACE_FILE"
echo "Output: $RA_DIR"
echo ""

# ── Stage 1: optrace -> trie_traversal_optrace.csv ───────────────────────────
echo "══════════════════════════════════════════════════════"
echo "[1/2] Stage 1: optrace -> trie_traversal_optrace.csv"
echo "══════════════════════════════════════════════════════"
"$STAGE1_BIN" "$TRACE_FILE" "$RA_DIR"
echo ""
if [ ! -f "$TRAV_CSV" ]; then
  echo "Error: stage 1 did not produce $TRAV_CSV"
  exit 1
fi

# ── Stage 2: trie_traversal_optrace.csv -> read_ops_analyzed.csv ─────────────
echo "══════════════════════════════════════════════════════"
echo "[2/2] Stage 2: trie_traversal_optrace.csv -> read_ops_analyzed.csv"
echo "══════════════════════════════════════════════════════"
"$STAGE2_BIN" "$TRAV_CSV" "$READ_OPS_CSV"
echo ""

echo "[done] Results in: $RA_DIR"
echo "  Stage 1: $TRAV_CSV"
echo "  Stage 2: $READ_OPS_CSV"
