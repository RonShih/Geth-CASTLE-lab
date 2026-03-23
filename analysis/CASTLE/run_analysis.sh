#!/bin/bash
# run_analysis.sh — Run RA analysis + trie node stats plotting
#
# Usage:
#   ./run_analysis.sh                    # analyze latest files
#   ./run_analysis.sh 11350800_11351000  # analyze a specific block range

set -e

EXEC_DIR=~/ethereum/execution
ANALYSIS_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$ANALYSIS_DIR"

BLOCK_RANGE="$1"

# ── Helper: find latest file matching prefix (+ optional block range) ────────
find_file() {
  local prefix="$1"
  if [ -n "$BLOCK_RANGE" ]; then
    ls -t "$EXEC_DIR/${prefix}_${BLOCK_RANGE}"* 2>/dev/null | head -1
  else
    ls -t "$EXEC_DIR/${prefix}_"* 2>/dev/null | head -1
  fi
}

# ── Ensure RA binary is built ────────────────────────────────────────────────
PARENT_DIR="$(dirname "$ANALYSIS_DIR")"
if [ ! -f "$PARENT_DIR/bin/analysisReadAmplification" ]; then
  echo "Binaries not found. Building..."
  bash "$PARENT_DIR/build.sh" build
fi

# ── Resolve files ────────────────────────────────────────────────────────────
TRACE_FILE=$(find_file "blktrace")
TRIE_FILE=$(find_file "trie_node_stats")

if [ -z "$TRACE_FILE" ] && [ -z "$TRIE_FILE" ]; then
  echo "Error: no blktrace or trie_node_stats file found."
  exit 1
fi

# Derive block range from whichever file we found
if [ -n "$TRACE_FILE" ]; then
  RANGE=$(basename "$TRACE_FILE" | sed 's/^blktrace_\([0-9]*_[0-9]*\).*/\1/')
elif [ -n "$TRIE_FILE" ]; then
  RANGE=$(basename "$TRIE_FILE" | sed 's/^trie_node_stats_\([0-9]*_[0-9]*\).*/\1/')
fi
OUTPUT_DIR="./raOutput_${RANGE}"

echo "Output directory: $OUTPUT_DIR"
echo ""

# ── 1. Read Amplification analysis ───────────────────────────────────────────
if [ -n "$TRACE_FILE" ]; then
  echo "══════════════════════════════════════════════════════"
  echo "[1/2] Read Amplification Analysis"
  echo "  Input:  $TRACE_FILE"
  echo "  Output: $OUTPUT_DIR"
  echo "══════════════════════════════════════════════════════"
  "$PARENT_DIR/bin/analysisReadAmplification" "$TRACE_FILE" "$OUTPUT_DIR"
  echo ""
else
  echo "[1/2] Skipping RA analysis: no blktrace file found."
fi

# ── 2. Trie node stats: copy CSV + plot ──────────────────────────────────────
if [ -n "$TRIE_FILE" ]; then
  mkdir -p "$OUTPUT_DIR"
  echo "══════════════════════════════════════════════════════"
  echo "[2/2] Trie Node Stats"
  echo "  Input:  $TRIE_FILE"
  echo "  Output: $OUTPUT_DIR"
  echo "══════════════════════════════════════════════════════"
  cp "$TRIE_FILE" "$OUTPUT_DIR/"
  echo "  Copied: $(basename "$TRIE_FILE")"
  python3 "$ANALYSIS_DIR/plotTrieNodeStats.py" "$TRIE_FILE" "$OUTPUT_DIR"
  echo ""
else
  echo "[2/2] Skipping trie analysis: no trie_node_stats file found."
fi

echo "[done] Results in: $OUTPUT_DIR"
