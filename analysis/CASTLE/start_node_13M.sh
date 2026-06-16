#!/bin/bash
# start_node_13M.sh — Build geth, launch Geth + Prysm, sync to block 13M and stop
#
# Trace collection: 12.7M ~ 13M (set in globalTraceLog.go)
# After geth stops, backups state to D drive for future measurements.
#
# Usage:
#   ./start_node_13M.sh                 # full flow: build → geth → prysm → backup
#   SKIP_BUILD=true ./start_node_13M.sh # skip build step
#
# View logs:
#   tail -f ~/ethereum/logs/latest/geth.log
#   tail -f ~/ethereum/logs/latest/prysm.log

set -e

GETH_REPO=~/Geth-CASTLE-Lab
EXEC_DIR=~/ethereum/execution
GETH_DATADIR=$EXEC_DIR/data
CONS_DIR=~/ethereum/consensus
LOG_DIR=~/ethereum/logs/$(date +%Y%m%d_%H%M%S)
TRACE_DIR=/mnt/d/castle_trace

# ── 0. Clean up stale processes ──────────────────────────────────────────────
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
# Remove stale lock file if no geth process is running
if [ -f "$EXEC_DIR/data/geth/LOCK" ]; then
  echo "[cleanup] Removing stale LOCK file..."
  rm -f "$EXEC_DIR/data/geth/LOCK"
fi

# ── 1. Build ─────────────────────────────────────────────────────────────────
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
mkdir -p "$LOG_DIR"
mkdir -p "$TRACE_DIR"
ln -sfn "$LOG_DIR" ~/ethereum/logs/latest
echo "[2/4] Logs:  ~/ethereum/logs/latest/"
echo "       Trace: $TRACE_DIR"

# ── 3. Start geth ────────────────────────────────────────────────────────────
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

# Pin Prysm version to avoid auto-upgrade breaking checkpoint sync
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

BACKUP_DIR=/mnt/d/geth_state_backup_13M

echo ""
echo "══════════════════════════════════════════════════════"
echo "  Nodes running in background."
echo "  Trace range: 12,700,000 ~ 13,000,000"
echo "  Geth stops at block 13,000,000"
echo "  Trace output: $TRACE_DIR"
echo "  State backup: $BACKUP_DIR"
echo "  View logs:"
echo "    tail -f ~/ethereum/logs/latest/geth.log"
echo "    tail -f ~/ethereum/logs/latest/prysm.log"
echo "  Waiting for geth to exit..."
echo "══════════════════════════════════════════════════════"
echo ""

# ── 5. Wait for geth to finish → backup state → stop prysm ─────────────────
wait $GETH_PID
echo ""
echo "[post] geth exited at block 13M."

# Run analysis with trace files from D drive, output also to D drive
echo "[post] Running analysis..."
cd "$GETH_REPO/analysis/CASTLE"
EXEC_DIR="$TRACE_DIR" OUTPUT_BASE_DIR="$TRACE_DIR" bash run_analysis.sh
echo "[post] Analysis done."

echo "[post] Stopping prysm..."
kill $PRYSM_PID 2>/dev/null
wait $PRYSM_PID 2>/dev/null || true

# Backup state to D drive
echo "[post] Backing up state to $BACKUP_DIR ..."
rsync -a --progress "$GETH_DATADIR/" "$BACKUP_DIR/"
echo "[post] State backup complete."

echo "[done] All finished."
echo "  Logs:     $LOG_DIR"
echo "  Trace:    $TRACE_DIR"
echo "  Backup:   $BACKUP_DIR"
echo "  Analysis: $TRACE_DIR/raOutput_*  (via run_analysis.sh)"
