package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {

	if len(os.Args) < 2 {
		fmt.Println("Usage: program <log_files_list_path>")
		return
	}
	logFilePath := os.Args[1]
	fmt.Println("Processing log file list:", logFilePath)
	inputFileList, err := os.Open(logFilePath)
	if err != nil {
		fmt.Println("Error opening file list:", logFilePath)
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

	// A map to store the aggregated counts: category -> opType -> count
	aggregatedData := make(map[string]map[string]uint64)

	// Process each file
	for fileName := range fileList {
		processFile(fileName, aggregatedData)
	}
	// Output the aggregated data
	printAggregatedData(aggregatedData)
}

// processFile processes a single file and aggregates data
func processFile(fileName string, aggregatedData map[string]map[string]uint64) error {
	file, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", fileName, err)
	}
	defer file.Close()
	totalOpCount := 0
	scanner := bufio.NewScanner(file)
	var currentCategory string
	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// If the line starts with "Category", it's a new category
		if strings.HasPrefix(line, "Category: ") {
			currentCategory = strings.TrimPrefix(line, "Category: ")
			// Initialize the category in the map if not already present
			if _, exists := aggregatedData[currentCategory]; !exists {
				aggregatedData[currentCategory] = make(map[string]uint64)
			}
		} else if strings.HasPrefix(line, "  OPType: ") {
			// Extract the OPType and Count
			parts := strings.Fields(line)
			if len(parts) != 4 {
				continue // skip invalid lines
			}
			opType := strings.Trim(parts[1], ",")
			count := parseCount(parts[3])

			// Aggregate the count for the current category and OPType
			aggregatedData[currentCategory][opType] += count
			totalOpCount += int(count)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file %s: %v", fileName, err)
	}
	fmt.Printf("%d\n", totalOpCount)
	return nil
}

// parseCount parses the count from the string and returns it as uint64
func parseCount(countStr string) uint64 {
	var count uint64
	fmt.Sscanf(countStr, "%d", &count)
	return count
}

// printAggregatedData prints the aggregated data in the required format
func printAggregatedData(aggregatedData map[string]map[string]uint64) {
	fmt.Println("Count of KV operations:")
	for category, opTypes := range aggregatedData {
		fmt.Printf("Category: %s\n", category)
		for opType, count := range opTypes {
			fmt.Printf("  OPType: %s, Count: %d\n", opType, count)
		}
	}
}
