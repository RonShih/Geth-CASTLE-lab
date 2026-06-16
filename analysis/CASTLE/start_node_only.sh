#!/bin/bash
# start_node_only.sh — Build geth, launch Geth + Prysm, just collect trace.
#
# Compared to start_node_13M.sh / start_node_20M.sh: NO post-processing.
# After Geth exits at the configured target block, this script just stops
# Prysm and ends. Use cases:
#   - You changed instrumentation (e.g. new SSTableProbe events) and the
#     existing analysis pipeline doesn't yet parse the new fields.
#   - You want fast turnaround on collection, then run analysis manually
#     later (it reads the saved trace file in $TRACE_DIR).
#   - You don't want to spend ~1 hour rsync-backing-up state every run.
#
# The trace target block range is set in common/globalTraceLog.go.
# After this script finishes, run analysis manually:
#   ./bin/analysisReadAmplification /mnt/d/castle_trace/optrace_<range>_<ts> \
#       /mnt/d/castle_trace/raOutput_<range>/optrace_analysis
# State backup (if you want one) via:
#   ./backup_state.sh backup <name>
#
# Usage:
#   ./start_node_only.sh                 # full flow: build → geth → prysm → wait → done
#   SKIP_BUILD=true ./start_node_only.sh # skip build step
#
# View logs while running:
#   tail -f ~/ethereum/logs/latest/geth.log
#   tail -f ~/ethereum/logs/latest/prysm.log

set -e

GETH_REPO=~/Geth-CASTLE-Lab
EXEC_DIR=~/ethereum/execution
CONS_DIR=~/ethereum/consensus
LOG_DIR=~/ethereum/logs/$(date +%Y%m%d_%H%M%S)
TRACE_DIR=/mnt/d/castle_trace

# ── 0. Clean up stale processes ──────────────────────────────────────────────
# Geth holds a LOCK file under data/geth/. If a previous run was killed
# uncleanly, the LOCK is left behind and geth refuses to start.
if pgrep -x geth > /dev/null; then
  echo "[cleanup] Killing stale geth (PID: $(pgrep -x geth))..."
  pkill -x geth
  sleep 2
fi
if pgrep -f "prysm.sh|beacon-chain" > /dev/null; then
  echo "[cleanup] Killing stale prysm (PID: $(pgrep -f 'prysm.sh|beacon-chain'))..."
  pkill -f "prysm.sh|beacon-chain"
  sleep 2
fi
if [ -f "$EXEC_DIR/data/geth/LOCK" ]; then
  echo "[cleanup] Removing stale LOCK file..."
  rm -f "$EXEC_DIR/data/geth/LOCK"
fi

# ── 1. Build ─────────────────────────────────────────────────────────────────
# Geth's Makefile shells out to `go run build/ci.go install ./cmd/geth`. If
# you invoke this script through a wrapper that doesn't have go in PATH,
# build will fail with "go: Command not found". Either:
#   - export PATH=/usr/local/go/bin:$PATH   in the calling shell, or
#   - SKIP_BUILD=true ./start_node_only.sh  if the binary is already built.
if [ "${SKIP_BUILD}" != "true" ]; then
  echo "[1/4] Building geth..."
  cd "$GETH_REPO"
  make
  NEW_BIN="$GETH_REPO/build/bin/geth"
  OLD_BIN="$EXEC_DIR/geth"
  if ! cmp -s "$NEW_BIN" "$OLD_BIN"; then
    cp "$NEW_BIN" "$OLD_BIN"
    echo "      Binary updated."
  else
    echo "      Binary unchanged, skipping copy."
  fi
else
  echo "[1/4] Skipping build (SKIP_BUILD=true)"
fi

# ── 2. Prepare log & trace directories ──────────────────────────────────────
# ~/ethereum/logs/latest is a symlink that always points to this run's logs.
# The trace dir is the destination written to from globalTraceLog.go.
mkdir -p "$LOG_DIR"
mkdir -p "$TRACE_DIR"
ln -sfn "$LOG_DIR" ~/ethereum/logs/latest
echo "[2/4] Logs:  ~/ethereum/logs/latest/"
echo "       Trace: $TRACE_DIR"

# ── 3. Start geth ────────────────────────────────────────────────────────────
# Flags:
#   --cache.noprefetch  Disable Geth's external block prefetcher so the trace
#                       reflects only the in-process trie prefetcher behavior.
#   --syncmode full     Replay execution; required for trace generation.
#   --authrpc.jwtsecret The shared secret with Prysm for engine API auth.
# GOMAXPROCS=1 limits the Go runtime to a single OS thread; goroutines still
# multiplex but execute serially, which makes traces easier to interpret.
echo "[3/4] Starting geth..."
cd "$EXEC_DIR"
GOMAXPROCS=1 stdbuf -oL ./geth \
  --cache.noprefetch \
  --mainnet \
  --datadir ./data \
  --syncmode full \
  --http \
  --http.api eth,net,engine,admin \
  --authrpc.jwtsecret ../jwt.hex \
  > "$LOG_DIR/geth.log" 2>&1 &
GETH_PID=$!
echo "      geth PID=$GETH_PID"

# ── 4. Wait for geth authrpc, then start prysm ──────────────────────────────
# Prysm pushes execution payloads to Geth via authrpc on port 8551. If we
# start prysm before geth's authrpc is up, prysm will fail to drive geth.
echo "      Waiting for geth authrpc (port 8551)..."
for i in $(seq 1 60); do
  if ss -tlnp | grep -q ':8551'; then
    echo "      geth authrpc ready."
    break
  fi
  if ! kill -0 "$GETH_PID" 2>/dev/null; then
    echo "ERROR: geth exited before authrpc was ready. Check log:"
    echo "  tail ~/ethereum/logs/latest/geth.log"
    exit 1
  fi
  sleep 2
done

echo "[4/4] Starting prysm..."
cd "$CONS_DIR"
# Prysm version is pinned because newer versions occasionally change the
# checkpoint-sync URL contract and break startup.
USE_PRYSM_VERSION=v7.1.2 ./prysm.sh beacon-chain \
  --datadir ./data \
  --execution-endpoint=http://localhost:8551 \
  --mainnet \
  --jwt-secret=../jwt.hex \
  --checkpoint-sync-url=https://beaconstate.info \
  --genesis-beacon-api-url=https://beaconstate.info \
  > "$LOG_DIR/prysm.log" 2>&1 &
PRYSM_PID=$!
echo "      prysm PID=$PRYSM_PID"

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Nodes running. Will wait for geth to exit at the"
echo "  configured target end block (set in globalTraceLog.go)."
echo "  Trace output: $TRACE_DIR"
echo "  View logs:"
echo "    tail -f ~/ethereum/logs/latest/geth.log"
echo "    tail -f ~/ethereum/logs/latest/prysm.log"
echo "══════════════════════════════════════════════════════"
echo ""

# ── 5. Wait for geth to finish → stop prysm → done ─────────────────────────
# No analysis, no backup. Run those manually after this script returns:
#   <bin>/analysisReadAmplification <trace> <output_dir>
#   ./backup_state.sh backup <name>
wait $GETH_PID
echo ""
echo "[post] geth exited. Stopping prysm..."
kill $PRYSM_PID 2>/dev/null
wait $PRYSM_PID 2>/dev/null || true

echo "[done] Trace collection finished."
echo "  Logs:  $LOG_DIR"
echo "  Trace: $TRACE_DIR"
echo
echo "  Next steps (manual):"
echo "    1. Analysis:   ${GETH_REPO}/analysis/bin/analysisReadAmplification \\"
echo "                       <trace-file> <output-dir>"
echo "    2. (Optional)  ${GETH_REPO}/analysis/CASTLE/backup_state.sh backup <name>"
