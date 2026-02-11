package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type OPType string

const (
	GET         OPType = "get"
	PUT         OPType = "put"
	BATCHED_PUT OPType = "batchput"
	UPDATE      OPType = "update"
	DELETE      OPType = "delete"
	SCAN        OPType = "scan"
)

type PrefixCategory struct {
	Prefix   string
	Category string
}

type OperationStats struct {
	OpTypeCount map[string]int
}

type OperationDistribution struct {
	GetOpDistributionCount         map[string]int
	PutOpDistributionCount         map[string]int
	PutNotBatchOpDistributionCount map[string]int
	UpdateOpDistributionCount      map[string]int
	DeleteOpDistributionCount      map[string]int
	ScanOpDistributionCountRange   map[string]int
}

var (
	stats          = make(map[string]*OperationStats)
	opDistribution = make(map[string]*OperationDistribution)
	hexPrefixes    = []PrefixCategory{
		{"7365637572652d6b65792d", "PreimagePrefix"},
		{"657468657265756d2d636f6e6669672d", "ConfigPrefix"},
		{"657468657265756d2d67656e657369732d", "GenesisPrefix"},
		{"636874526f6f7456322d", "ChtPrefix"},
		{"636874496e64657856322d", "ChtIndexTablePrefix"},
		{"6669786564526f6f742d", "FixedCommitteeRootKey"},
		{"636f6d6d69747465652d", "SyncCommitteeKey"},
		{"6368742d", "ChtTablePrefix"},
		{"626c74526f6f742d", "BloomTriePrefix"},
		{"626c74496e6465782d", "BloomTrieIndexPrefix"},
		{"626c742d", "BloomTrieTablePrefix"},
		{"636c697175652d", "CliqueSnapshotPrefix"},
		{"7570646174652d", "BestUpdateKey"},
		{"536e617073686f7453796e63537461747573", "SnapshotSyncStatusKey"},
		{"536e617073686f7444697361626c6564", "SnapshotDisabledKey"},
		{"536e617073686f74526f6f74", "SnapshotRootKey"},
		{"536e617073686f744a6f75726e616c", "SnapshotJournalKey"},
		{"536e617073686f7447656e657261746f72", "SnapshotGeneratorKey"},
		{"536e617073686f745265636f76657279", "SnapshotRecoveryKey"},
		{"536b656c65746f6e53796e63537461747573", "SkeletonSyncStatusKey"},
		{"5472696553796e63", "FastTrieProgressKey"},
		{"547269654a6f75726e616c", "TrieJournalKey"},
		{"5472616e73616374696f6e496e6465785461696c", "TxIndexTailKey"},
		{"466173745472616e73616374696f6e4c6f6f6b75704c696d6974", "FastTxLookupLimitKey"},
		{"496e76616c6964426c6f636b", "BadBlockKey"},
		{"756e636c65616e2d73687574646f776e", "UncleanShutdownKey"},
		{"657468322d7472616e736974696f6e", "TransitionStatusKey"},
		{"536e617053796e63537461747573", "SnapSyncStatusFlagKey"},
		{"446174616261736556657273696f6e", "DatabaseVersionKey"},
		{"4c617374486561646572", "HeadHeaderKey"},
		{"4c617374426c6f636b", "HeadBlockKey"},
		{"4c61737446617374", "HeadFastBlockKey"},
		{"4c61737446696e616c697a6564", "HeadFinalizedBlockKey"},
		{"4c61737453746174654944", "PersistentStateIDKey"},
		{"4c6173745069766f74", "LastPivotKey"},
		{"69", "BloomBitsIndexPrefix"},
		{"68", "HeaderPrefix"},
		{"74", "HeaderTDSuffix"},
		{"6e", "HeaderHashSuffix"},
		{"48", "HeaderNumberPrefix"},
		{"62", "BlockBodyPrefix"},
		{"72", "BlockReceiptsPrefix"},
		{"6c", "TxLookupPrefix"},
		{"42", "BloomBitsPrefix"},
		{"61", "SnapshotAccountPrefix"},
		{"6f", "SnapshotStoragePrefix"},
		{"63", "CodePrefix"},
		{"53", "SkeletonHeaderPrefix"},
		{"41", "TrieNodeAccountPrefix"},
		{"4f", "TrieNodeStoragePrefix"},
		{"4c", "StateIDPrefix"},
		{"76", "VerklePrefix"},
	}

	targetDistributionCountCategory = map[string]bool{
		"PreimagePrefix":        true,
		"ConfigPrefix":          true,
		"GenesisPrefix":         true,
		"ChtPrefix":             true,
		"ChtIndexTablePrefix":   true,
		"FixedCommitteeRootKey": true,
		"SyncCommitteeKey":      true,
		"ChtTablePrefix":        true,
		"BloomTriePrefix":       true,
		"BloomTrieIndexPrefix":  true,
		"BloomTrieTablePrefix":  true,
		"CliqueSnapshotPrefix":  true,
		"BestUpdateKey":         true,
		"SnapshotSyncStatusKey": true,
		"SnapshotDisabledKey":   true,
		"SnapshotRootKey":       true,
		"SnapshotJournalKey":    true,
		"SnapshotGeneratorKey":  true,
		"SnapshotRecoveryKey":   true,
		"SkeletonSyncStatusKey": true,
		"FastTrieProgressKey":   true,
		"TrieJournalKey":        true,
		"TxIndexTailKey":        true,
		"BadBlockKey":           true,
		"UncleanShutdownKey":    true,
		"TransitionStatusKey":   true,
		"SnapSyncStatusFlagKey": true,
		"DatabaseVersionKey":    true,
		"HeadHeaderKey":         true,
		"HeadBlockKey":          true,
		"HeadFastBlockKey":      true,
		"HeadFinalizedBlockKey": true,
		"PersistentStateIDKey":  true,
		"LastPivotKey":          true,
		"BloomBitsIndexPrefix":  true,
		"HeaderPrefix":          true,
		"HeaderTDSuffix":        true,
		"HeaderHashSuffix":      true,
		"HeaderNumberPrefix":    true,
		"BlockBodyPrefix":       true,
		"BlockReceiptsPrefix":   true,
		"TxLookupPrefix":        true,
		"BloomBitsPrefix":       true,
		"SnapshotAccountPrefix": true,
		"SnapshotStoragePrefix": true,
		"CodePrefix":            true,
		"SkeletonHeaderPrefix":  true,
		"TrieNodeAccountPrefix": true,
		"TrieNodeStoragePrefix": true,
		"StateIDPrefix":         true,
		"VerklePrefix":          true,
	}
)

func processLogFile(filePath, category, opType string) {
	file, err := os.Open(filePath)
	if err != nil {
		panic(fmt.Sprintf("Failed to open file: %s", filePath))
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var lineCount uint64
	lineCount = 0

	for {
		line, err := reader.ReadString('\n') // Read until newline
		if err != nil {
			if err == io.EOF {
				fmt.Println("End of file reached")
				break
			}
			fmt.Println("Error reading file:", err)
			return
		}

		lineCount++
		// split the line into three part by \t
		// 1. ID
		// 2. ke
		// 3. count
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			fmt.Println("Invalid line:", line)
			continue
		}
		key := parts[1]
		// remove \n from the parts[2]
		parts[2] = strings.TrimSuffix(parts[2], "\n")
		count, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			fmt.Println("Error parsing count:", err)
			continue
		}
		// fmt.Println("key:", key, "count:", count)

		// Update stats
		if _, exists := stats[category]; !exists {
			stats[category] = &OperationStats{OpTypeCount: make(map[string]int)}
		}
		stats[category].OpTypeCount[opType] += int(count)

		// Update operation distribution
		if _, exists := opDistribution[category]; !exists {
			opDistribution[category] = &OperationDistribution{
				GetOpDistributionCount:         make(map[string]int),
				PutOpDistributionCount:         make(map[string]int),
				PutNotBatchOpDistributionCount: make(map[string]int),
				UpdateOpDistributionCount:      make(map[string]int),
				DeleteOpDistributionCount:      make(map[string]int),
				ScanOpDistributionCountRange:   make(map[string]int),
			}
		}
		dist := opDistribution[category]
		switch opType {
		case "get":
			dist.GetOpDistributionCount[key] += int(count)
		case "batchput":
			dist.PutOpDistributionCount[key] += int(count)
		case "put":
			dist.PutNotBatchOpDistributionCount[key] += int(count)
		case "update":
			dist.UpdateOpDistributionCount[key] += int(count)
		case "delete":
			dist.DeleteOpDistributionCount[key] += int(count)
		case "scan":
			dist.ScanOpDistributionCountRange[key] += int(count)
		}
	}
	fmt.Printf("\rProcessed a total of %d lines.\n", lineCount)
}

func printDistributionStats(opMap map[string]int, category, opType string) {
	sortedOps := make([]struct {
		Key   string
		Count int
	}, 0, len(opMap))
	for k, v := range opMap {
		sortedOps = append(sortedOps, struct {
			Key   string
			Count int
		}{k, v})
	}
	sort.Slice(sortedOps, func(i, j int) bool {
		return sortedOps[i].Count > sortedOps[j].Count
	})

	if len(sortedOps) == 0 {
		fmt.Println("No operations found for category:", category, "opType:", opType)
		return
	} else {
		fmt.Println(len(sortedOps), " Operations found for category:", category, "opType:", opType)
	}
	fileName := category + "_" + opType + "_with_key_dis.txt"
	fileNameWithoutKey := category + "_" + opType + "_without_key_dis.txt"
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file (with key): %s\n", fileName)
		return
	}
	defer file.Close()
	fileWithoutKey, err := os.Create(fileNameWithoutKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file (without key): %s\n", fileNameWithoutKey)
		return
	}
	defer fileWithoutKey.Close()

	_, _ = fileWithoutKey.WriteString("ID\tCount\n")
	for id, entry := range sortedOps {
		_, _ = fileWithoutKey.WriteString(fmt.Sprintf("%d\t%d\n", id+1, entry.Count))
	}

	_, _ = file.WriteString("ID\tKey\tCount\n")
	for id, entry := range sortedOps {
		_, _ = file.WriteString(fmt.Sprintf("%d\t%s\t%d\n", id+1, entry.Key, entry.Count))
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: program <log_files_list_path> <category> <op_type>")
		return
	}
	logFilePath := os.Args[1]
	category := os.Args[2]
	opType := os.Args[3]
	fmt.Println("Processing log file:", logFilePath)
	inputFileList, err := os.Open(logFilePath)
	if err != nil {
		fmt.Println("Error opening file:", logFilePath)
		return
	}
	defer inputFileList.Close()
	lineReader := bufio.NewReader(inputFileList)
	// create a map to store the files that are being processed
	fileList := make(map[string]bool)
	// read the file line by line
	for {
		line, err := lineReader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println("End of file reached")
				break
			}
			fmt.Println("Error reading file:", err)
			return
		}
		// remove the newline character
		line = strings.TrimSuffix(line, "\n")
		// Put the file in the map
		fileList[line] = true
	}
	// process the files
	for currentLogFilePath := range fileList {
		processLogFile(currentLogFilePath, category, opType)
	}
	switch opType {
	case "get":
		printDistributionStats(opDistribution[category].GetOpDistributionCount, category, opType)
	case "put":
		printDistributionStats(opDistribution[category].PutNotBatchOpDistributionCount, category, opType)
	case "batchput":
		printDistributionStats(opDistribution[category].PutOpDistributionCount, category, opType)
	case "update":
		printDistributionStats(opDistribution[category].UpdateOpDistributionCount, category, opType)
	case "delete":
		printDistributionStats(opDistribution[category].DeleteOpDistributionCount, category, opType)
	case "scan":
		printDistributionStats(opDistribution[category].ScanOpDistributionCountRange, category, opType)
	}

}
