#!/bin/bash

path_to_trace="$1"

if [ -z "$path_to_trace" ]; then
  echo "Usage: $0 <path_to_trace>"
  exit 1
fi

# Check if the file exists
if [ ! -f "$path_to_trace" ]; then
  echo "Trace file not found: $path_to_trace"
  exit 1
fi


categorySet=("PreimagePrefix"        "ConfigPrefix"          "GenesisPrefix"         "ChtPrefix"             "ChtIndexTablePrefix"   "FixedCommitteeRootKey" "SyncCommitteeKey"      "ChtTablePrefix"        "BloomTriePrefix"       "BloomTrieIndexPrefix"  "BloomTrieTablePrefix"  "CliqueSnapshotPrefix"  "BestUpdateKey"         "SnapshotSyncStatusKey" "SnapshotDisabledKey"   "SnapshotRootKey"       "SnapshotJournalKey"    "SnapshotGeneratorKey"  "SnapshotRecoveryKey"   "SkeletonSyncStatusKey" "FastTrieProgressKey"   "TrieJournalKey"        "TxIndexTailKey"        "BadBlockKey"           "UncleanShutdownKey"    "TransitionStatusKey"   "SnapSyncStatusFlagKey" "DatabaseVersionKey"    "HeadHeaderKey"         "HeadBlockKey"          "HeadFastBlockKey"      "HeadFinalizedBlockKey" "PersistentStateIDKey"  "LastPivotKey"          "BloomBitsIndexPrefix"  "HeaderPrefix"          "HeaderTDSuffix"        "HeaderHashSuffix"      "HeaderNumberPrefix"    "BlockBodyPrefix"       "BlockReceiptsPrefix"   "TxLookupPrefix"        "BloomBitsPrefix"       "SnapshotAccountPrefix" "SnapshotStoragePrefix" "CodePrefix"            "SkeletonHeaderPrefix"  "TrieNodeAccountPrefix" "TrieNodeStoragePrefix" "StateIDPrefix"         "VerklePrefix")
opTypeSet=("get" "put" "batchput" "update" "delete" "scan")

tmp_dir="kvOperationDistributionTemp"
if [ ! -d "$tmp_dir" ]; then
    mkdir "$tmp_dir"
else
    rm -r "$tmp_dir"
    mkdir "$tmp_dir"
fi

# Count the distribution of KV operations
./bin/countOpDistribution "$path_to_trace" 10 1 20500000 20500010 
# ./bin/countOpDistribution "$path_to_trace" 1000 1 20500000 20501000

mv ./countKVDist-* $tmp_dir/
mv ./distribution-* $tmp_dir/

PATH_TO_RESULTS_DIR="$tmp_dir"

# Get file prefix from the results dir that start with "distribution-", cut the content after the first underscore, and sort them
filePathPrefixSet=()
for file in "$PATH_TO_RESULTS_DIR"/distribution-*; do
    if [[ -f "$file" ]]; then
        filename=$(basename "$file")
        prefix=$(echo "${filename#distribution-}" | cut -d'_' -f1-2)
        prefix="distribution-"$prefix
        echo $prefix
        filePathPrefixSet+=("$prefix")
    fi
done
mapfile -t filePathPrefixSet < <(printf "%s\n" "${filePathPrefixSet[@]}" | sort -u)

for category in "${categorySet[@]}"; do
    for opType in "${opTypeSet[@]}"; do   
    echo "Processing $category, $opType"
    canContinue=false
        currentLogFileName="processing-${category}_${opType}.txt"
        for filePathPrefix in "${filePathPrefixSet[@]}"; do
            if [ ! -f "${PATH_TO_RESULTS_DIR}/${filePathPrefix}_${category}_${opType}_dis.txt" ]; then
                echo "File ${PATH_TO_RESULTS_DIR}/${filePathPrefix}_${category}_${opType}_dis.txt does not exist"
                continue
            fi
            echo "${PATH_TO_RESULTS_DIR}/${filePathPrefix}_${category}_${opType}_dis.txt" >> "$currentLogFileName"
            canContinue=true
        done
        if [ "$canContinue" = false ]; then
            continue
        fi
        cat "$currentLogFileName"
        ./bin/mergeOpDist "$currentLogFileName" "$category" "$opType"
        rm "$currentLogFileName"
    done
done

if [ ! -d "${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution" ]; then
    mkdir "${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution"
else
    rm -r "${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution"
    mkdir "${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution"
fi

mv ./*.txt "${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution"

# Merge the count of each operation
# Get file prefix from the results dir that start with "countDist-", cut the content after the first underscore, and sort them
overallCountfilePathPrefixSet=()
for file in "$PATH_TO_RESULTS_DIR"/countKVDist-*; do
    if [[ -f "$file" ]]; then
        filename=$(basename "$file")
        prefix=$(echo "${filename#countKVDist-}" | cut -d'_' -f1-2)
        prefix="countKVDist-"$prefix
        echo $prefix
        overallCountfilePathPrefixSet+=("$prefix")
    fi
done

mapfile -t overallCountfilePathPrefixSet < <(printf "%s\n" "${overallCountfilePathPrefixSet[@]}" | sort -u)
for overallCountfilePathPrefix in "${overallCountfilePathPrefixSet[@]}"; do
    if [ ! -f "${PATH_TO_RESULTS_DIR}/${overallCountfilePathPrefix}" ]; then
        echo "File ${PATH_TO_RESULTS_DIR}/${overallCountfilePathPrefix} does not exist"
        continue
    fi
    echo "${PATH_TO_RESULTS_DIR}/${overallCountfilePathPrefix}" >> "processing-count.txt"
done

./bin/countMergedOp "processing-count.txt" > "./mergedKVOpCount.txt"
rm "processing-count.txt"
mv ${PATH_TO_RESULTS_DIR}/mergedKVOpDistribution .
echo "Done"
echo -e "\n\nResults:"
echo -e "\tKV operation count is saved in ./mergedKVOpCount.txt"
echo -e "\tKV operation distribution (of each KV class and KV operation type) is saved in ./mergedOpDistribution"

rm -rf $tmp_dir