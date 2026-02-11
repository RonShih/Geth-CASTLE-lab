#!/bin/bash
path_to_trace="$1"
distances="${2:-"0,1,4,16,64,256,1024"}"
starts_blocks="${3:-"20500000"}"
ends_blocks="${4:-"20501000"}"

if [ -z "$path_to_trace" ]; then
  echo "Usage: $0 <path_to_trace>"
  exit 1
fi

# Check if the file exists
if [ ! -f "$path_to_trace" ]; then
  echo "Trace file not found: $path_to_trace"
  exit 1
fi

tmp_dir="readCorrelationTemp"
final_output_dir="readCorrelationOutput"

if [ ! -d "$tmp_dir" ]; then
    mkdir "$tmp_dir"
else
    rm -r "$tmp_dir"
    mkdir "$tmp_dir"
fi

if [ ! -d "$final_output_dir" ]; then
    mkdir "$final_output_dir"
else
    rm -r "$final_output_dir"
    mkdir "$final_output_dir"
fi

./bin/collectCorrelation -logfiles="$path_to_trace" -outputdir="$tmp_dir" -distances="$distances" -starts="$starts_blocks" -ends="$ends_blocks" -optype="Get"

# find all files based on different distances
for distance in $(echo "$distances" | tr ',' ' '); do
    logFiles=$(find "$tmp_dir" -type f -name "rawFreq-*-Dist${distance}.log" | paste -sd, -)
    if [ -z "$logFiles" ]; then
        echo "No log files found for distance: $distance"
        continue
    fi
    echo "Processing distance: $distance with log files: $logFiles"
    ./bin/analysisCorrelation -distance="$distance" -logFiles="$logFiles" -outputPath="$final_output_dir"
done

rm -rf "$tmp_dir"