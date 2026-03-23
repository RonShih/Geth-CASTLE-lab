#!/bin/bash
# start_node.sh — Build geth, launch Geth + Prysm in background, then run analysis
#
# Usage:
#   ./start_node.sh                 # full flow: build → geth → prysm → analysis
#   SKIP_BUILD=true ./start_node.sh # skip build step
#
# View logs:
#   tail -f ~/ethereum/logs/latest/geth.log
#   tail -f ~/ethereum/logs/latest/prysm.log

set -e

GETH_REPO=~/Geth-MPT-db-test
EXEC_DIR=~/ethereum/execution
CONS_DIR=~/ethereum/consensus
LOG_DIR=~/ethereum/logs/$(date +%Y%m%d_%H%M%S)

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

# ── 2. Prepare log directory ─────────────────────────────────────────────────
mkdir -p "$LOG_DIR"
ln -sfn "$LOG_DIR" ~/ethereum/logs/latest
echo "[2/4] Logs: ~/ethereum/logs/latest/"

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
# To use latest version, remove USE_PRYSM_VERSION and uncomment the original:
#   ./prysm.sh beacon-chain \
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
echo "  Nodes running in background."
echo "  View logs:"
echo "    tail -f ~/ethereum/logs/latest/geth.log"
echo "    tail -f ~/ethereum/logs/latest/prysm.log"
echo "  Waiting for geth to exit before running analysis..."
echo "══════════════════════════════════════════════════════"
echo ""

# ── 5. Wait for geth to finish → run analysis → stop prysm ──────────────────
wait $GETH_PID
echo ""
echo "[post] geth exited. Running analysis..."
cd "$GETH_REPO/analysis/CASTLE"
bash run_analysis.sh

echo "[post] Analysis done. Stopping prysm..."
kill $PRYSM_PID 2>/dev/null
wait $PRYSM_PID 2>/dev/null || true

echo "[done] All finished. Logs in: $LOG_DIR"
