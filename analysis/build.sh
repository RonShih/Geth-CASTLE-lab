#!/bin/bash

ShouldInstall=$1
if [ "$ShouldInstall" == "install" ]; then
    go mod init eth
    go get github.com/syndtr/goleveldb/leveldb
    go get github.com/cockroachdb/pebble
    go get github.com/ethereum/go-ethereum/rlp
    go get github.com/syndtr/goleveldb
    go get github.com/getsentry/sentry-go@v0.27.0
    go get github.com/cockroachdb/pebble@v1.1.2
    go get golang.org/x/sys/unix
    go get golang.org/x/exp/rand
    go get github.com/cespare/xxhash/v2
    go get github.com/golang/protobuf/proto
    go get github.com/prometheus/client_model/go@v0.2.1-0.20210607210712-147c58e9608a
    go get github.com/golang/snappy
elif [ "$ShouldInstall" == "build" ]; then
    if [ ! -d "bin" ]; then
        mkdir bin
    else
        rm -rf bin/*
    fi
    # for KV size
    go build -o bin/countKVSizeDistribution analysisKVStoragePebble.go
    # for KV operation distribution
    go build -o bin/countOpDistribution analysisOpDistributionByBatch.go
    go build -o bin/mergeOpDist analysisOpDistributionMergeDistribution.go
    go build -o bin/countMergedOp analysisOpDistributionMergeCount.go
    # for updates detection
    go build -o bin/filterUpdates filterUpdates.go
    # for correlation
    go build -o bin/collectCorrelation collectCorrelation.go
    go build -o bin/analysisCorrelation analysisCorrelation.go
    # trie-traversal stage 1: optrace_* -> trie_traversal_optrace.csv
    go build -o bin/analysisReadAmplification CASTLE/code_optrace_analysis/analysis_optrace.go
    # trie-traversal stage 2: trie_traversal_optrace.csv -> read_ops_analyzed.csv
    go build -o bin/analysisTrieTraversalOptrace CASTLE/code_trie_traversal_optrace_analysis/analysis_trie_traversal_optrace.go
else
    echo "Usage: $0 install|build"
    echo "  install: Install the required Go modules and build the analysis tools."
    echo "  build: Build the analysis tools without installing modules."
    exit 1
fi
