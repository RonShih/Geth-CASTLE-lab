#!/bin/bash
# readAmplificationAnalysis.sh
# Analyze Read Amplification from a blktrace log file.
#
# Usage: ./readAmplificationAnalysis.sh [trace_file] [output_dir]
#
# Defaults:
#   trace_file : /home/ron/ethereum/execution/blktrace_11250000_11350000
#   output_dir : ./raOutput_11250000_11350000

# Auto-detect the most recent trace file
TRACE_FILE=$(ls -t /home/ron/ethereum/execution/blktrace_* 2>/dev/null | head -1)
if [ -z "$TRACE_FILE" ]; then
    echo "Error: no trace file found in /home/ron/ethereum/execution/"
    exit 1
fi
echo "Auto-detected latest trace file: $TRACE_FILE"
# Derive output dir from trace filename (e.g. blktrace_11350500_11350600_... -> raOutput_11350500_11350600)
BASENAME=$(basename "$TRACE_FILE")
BLOCK_RANGE=$(echo "$BASENAME" | sed 's/^blktrace_\([0-9]*_[0-9]*\).*/\1/')
OUTPUT_DIR=${2:-./raOutput_${BLOCK_RANGE}}

if [ ! -f "$TRACE_FILE" ]; then
    echo "Error: trace file not found: $TRACE_FILE"
    exit 1
fi

if [ ! -f "bin/analysisReadAmplification" ]; then
    echo "Binary not found. Building..."
    bash build.sh build
fi

echo "Analyzing $TRACE_FILE -> $OUTPUT_DIR"
./bin/analysisReadAmplification "$TRACE_FILE" "$OUTPUT_DIR"
