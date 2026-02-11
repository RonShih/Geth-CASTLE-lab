package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
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
	GetOpDistributionCount            	map[string]int
	PutOpDistributionCount         		map[string]int
	PutNotBatchOpDistributionCount 		map[string]int
	UpdateOpDistributionCount         	map[string]int
	DeleteOpDistributionCount         	map[string]int
	ScanOpDistributionCountRange      	map[string]int
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

func matchPrefix(key string) string {
	for _, prefix := range hexPrefixes {
		if strings.HasPrefix(key, prefix.Prefix) {
			// fmt.Print("Matched prefix: ", prefix.Prefix, " for key: ", key, "\n")
			return prefix.Category
		}
	}
	return "Unknown"
}

func parseLogLine(line string) (string, string, string, bool) {
	re := regexp.MustCompile(`OPType: (\w+(?: \w+)*), (?:key: ([a-fA-F0-9]+)|prefix: ([a-fA-F0-9]+))?`)
	matches := re.FindStringSubmatch(line)
	if matches == nil {
		return "", "", "", false
	}

	opType := matches[1]
	var key, category string

	// Always set key, preferring the 'key' value, and fallback to 'prefix'
	if matches[2] != "" {
		key = matches[2]
		category = matchPrefix(key) // You can implement matchPrefix as you did before
	} else if matches[3] != "" {
		key = matches[3]
		category = matchPrefix(key)
	} else {
		key = "" // Or whatever default value you want if neither is found
		category = "noPrefix"
	}

	return opType, category, key, true
}

func processLogFile(filePath string, progressInterval uint64, startBlockNumber, endBlockNumber, stepSize uint64) {
	file, err := os.Open(filePath)
	if err != nil {
		panic(fmt.Sprintf("Failed to open file: %s", filePath))
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var currentBlockID uint64
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
		// fmt.Println("Reading line:", line)
		// Loop until find a line that contains "Processing block"
		compareStr := "Processing block (start), ID: " + strconv.FormatUint(startBlockNumber, 10)
		if strings.Contains(line, compareStr) {
			fmt.Println("Found the first block that is larger than (", startBlockNumber, "), start processing")
			currentBlockID = uint64(startBlockNumber)
			break
		}
	}

	currentEndBlockNumber := startBlockNumber + stepSize
	currentStartBlockNumber := startBlockNumber

	for {
		fmt.Println("Start processing log file, start ID:", currentStartBlockNumber, "current end ID:", currentEndBlockNumber)

		var lineCount uint64
		start := time.Now()
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

			if lineCount%progressInterval == 0 {
				elapsed := time.Since(start).Seconds()
				fmt.Printf("\rProcessed %d lines, current block ID: %d, elapsed time: %.2fs", lineCount, currentBlockID, elapsed)
			}

			opType, category, key, parsed := parseLogLine(line)
			if !parsed {
				// Check if this is a block number line
				if strings.Contains(line, "Processing block (start)") {
					re := regexp.MustCompile(`ID:\s*(\d+)`)
					matches := re.FindStringSubmatch(line)
					if len(matches) > 1 {
						id, err := strconv.Atoi(matches[1])
						if err == nil {
							currentBlockID = uint64(id)
							if id > int(currentEndBlockNumber) {
								fmt.Println("Found the last block that is smaller than (", currentEndBlockNumber, "), stop processing")
								break
							}
						} else {
							fmt.Println("Error converting ID to integer:", err)
						}
					}
				}
				continue
			}

			// Update stats
			if _, exists := stats[category]; !exists {
				stats[category] = &OperationStats{OpTypeCount: make(map[string]int)}
			}
			stats[category].OpTypeCount[opType]++

			// Update operation distribution
			if _, exists := opDistribution[category]; !exists {
				opDistribution[category] = &OperationDistribution{
					GetOpDistributionCount:            make(map[string]int),
					PutOpDistributionCount:            make(map[string]int),
					PutNotBatchOpDistributionCount:    make(map[string]int),
					UpdateOpDistributionCount:         make(map[string]int),
					DeleteOpDistributionCount:         make(map[string]int),
					ScanOpDistributionCountRange:      make(map[string]int),
				}
			}
			dist := opDistribution[category]
			switch opType {
			case "Get":
				dist.GetOpDistributionCount[key]++
			case "BatchPut":
				dist.PutOpDistributionCount[key]++
			case "Put":
				dist.PutNotBatchOpDistributionCount[key]++
			case "Update":
				dist.UpdateOpDistributionCount[key]++
			case "BatchDelete":
				dist.DeleteOpDistributionCount[key]++
			case "NewIterator":
				dist.ScanOpDistributionCountRange[key]++
			}
		}
		outPutLogPath := "countKVDist-" + strconv.FormatUint(currentStartBlockNumber, 10) + "_" + strconv.FormatUint(currentEndBlockNumber, 10) + ".txt"
		filePrefix := "distribution-" + strconv.FormatUint(currentStartBlockNumber, 10) + "_" + strconv.FormatUint(currentEndBlockNumber, 10) + "_"
		fmt.Printf("\rProcessed a total of %d lines, write results into %s.\n", lineCount, outPutLogPath)
		file, err := os.Create(outPutLogPath)
		if err != nil {
			fmt.Println("Error creating output file:", outPutLogPath)
			return
		}
		defer file.Close()
		printStats(file, filePrefix)
		currentEndBlockNumber += stepSize
		currentStartBlockNumber += stepSize
		// Reset stats
		stats = make(map[string]*OperationStats)
		opDistribution = make(map[string]*OperationDistribution)
        if currentStartBlockNumber > endBlockNumber {
			fmt.Println("Stop processing due to reach end block ID")
            break
        }
	}
}

func toString(opType OPType) string {
	return string(opType)
}

func printDistributionStats(opMap map[string]int, category string, opType OPType, filePrefix string) {
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

	fileName := filePrefix + category + "_" + toString(opType) + "_dis.txt"
	file, err := os.Create(fileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %s\n", fileName)
		return
	}
	defer file.Close()

	_, _ = file.WriteString("ID\tKey\tCount\n")
	for id, entry := range sortedOps {
		_, _ = file.WriteString(fmt.Sprintf("%d\t%s\t%d\n", id+1, entry.Key, entry.Count))
	}
}

func printStats(outputFile *os.File, filePrefix string) {
	fmt.Fprintln(outputFile, "Count of KV operations:")
	for category, opStats := range stats {
		fmt.Fprintf(outputFile, "Category: %s\n", category)
		for opType, count := range opStats.OpTypeCount {
			fmt.Fprintf(outputFile, "  OPType: %s, Count: %d\n", opType, count)
		}
	}
	fmt.Fprintln(outputFile, "\n\nDistribution of KV operations:")
	for category, opDist := range opDistribution {
		fmt.Println("Category:", category)
		if len(opDist.GetOpDistributionCount) > 1 {
			fmt.Println("\tGet operation count:", len(opDist.GetOpDistributionCount))
			printDistributionStats(opDist.GetOpDistributionCount, category, GET, filePrefix)
		}
		if len(opDist.PutOpDistributionCount) > 1 {
			fmt.Println("\tPut operation count:", len(opDist.PutOpDistributionCount))
			printDistributionStats(opDist.PutOpDistributionCount, category, BATCHED_PUT, filePrefix)
		}
		if len(opDist.UpdateOpDistributionCount) > 1 {
			fmt.Println("\tUpdate operation count:", len(opDist.UpdateOpDistributionCount))
			printDistributionStats(opDist.UpdateOpDistributionCount, category, UPDATE, filePrefix)
		}
		if len(opDist.PutNotBatchOpDistributionCount) > 1 {
			fmt.Println("\tPut operation count:", len(opDist.PutNotBatchOpDistributionCount))
			printDistributionStats(opDist.PutNotBatchOpDistributionCount, category, PUT, filePrefix)
		}
		if len(opDist.DeleteOpDistributionCount) > 1 {
			fmt.Println("\tDelete operation count:", len(opDist.DeleteOpDistributionCount))
			printDistributionStats(opDist.DeleteOpDistributionCount, category, DELETE, filePrefix)
		}
		if len(opDist.ScanOpDistributionCountRange) > 1 {
			fmt.Println("\tScan operation count:", len(opDist.ScanOpDistributionCountRange))
			printDistributionStats(opDist.ScanOpDistributionCountRange, category, SCAN, filePrefix)
		}
	}
}

func main() {
	if len(os.Args) < 6 {
		fmt.Println("Usage: program <log_file_path> <batch_size_for_each_output> <print_progress_interval> <start_block_number> <end_block_number>")
		return
	}
	logFilePath := os.Args[1]
	stepSize, _ := strconv.ParseUint(os.Args[2], 10, 64)
	progressInterval, _ := strconv.ParseUint(os.Args[3], 10, 64)
	startBlockNumber, _ := strconv.ParseUint(os.Args[4], 10, 64)
	endBlockNumber, _ := strconv.ParseUint(os.Args[5], 10, 64)
	processLogFile(logFilePath, progressInterval, startBlockNumber, endBlockNumber, stepSize)
}
