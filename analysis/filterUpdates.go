package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/cockroachdb/pebble"
)

func parseLogLine(line string) (string, string) {
	re := regexp.MustCompile(`OPType: (\w+(?: \w+)*), (?:key: ([a-fA-F0-9]+)|prefix: ([a-fA-F0-9]+))?`)
	matches := re.FindStringSubmatch(line)
	if matches == nil {
		return "", ""
	}

	opType := matches[1]
	var key string

	// Always set key, preferring the 'key' value, and fallback to 'prefix'
	if matches[2] != "" {
		key = matches[2]
	} else if matches[3] != "" {
		key = matches[3]
	} else {
		key = ""
	}
	return opType, key
}

func main() {
	dbFile := os.Args[1]
	db, err := pebble.Open(dbFile, &pebble.Options{})
	if err != nil {
		log.Fatalf("Cannot open target database, err: %v\n", err)
	}
	defer db.Close()

	traceFile := os.Args[2]
	trace, err := os.Open(traceFile)
	if err != nil {
		log.Fatalf("Cannot open trace file, err: %v\n", err)
	}
	defer trace.Close()

	outputTraceFile := os.Args[3]
	outputTrace, err := os.Create(outputTraceFile)
	if err != nil {
		log.Fatalf("Cannot create output trace file, err: %v\n", err)
	}
	defer outputTrace.Close()

	const progressInterval = 1000

	// Build a set to record the newly writed keys
	keySet := make(map[string]struct{})

	fmt.Printf("Start processing KV operations\n")
	var lineCount uint64
	start := time.Now()
	lineCount = 0
	lineChangedToUpdate := 0
	lineNotChangedToUpdate := 0
	// scan lines in the trace file
	reader := bufio.NewReader(trace)
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
			fmt.Printf("\rProcessed %d lines, changed %d updates, keep %d writes, elapsed time: %.2fs", lineCount, lineChangedToUpdate, lineNotChangedToUpdate, elapsed)
		}
		opType, key := parseLogLine(line)
		if opType == "Put" || opType == "BatchPut" {
			// convert hex key to bytes
			keyBytes, err := hex.DecodeString(key)
			if err != nil {
				fmt.Println("Error decoding hex key:", err)
				continue
			}
			// check if the key exists in the database
			// if it does, write the line to the output trace file
			_, closer, err := db.Get(keyBytes)
			if err != nil {
				if _, exists := keySet[string(keyBytes)]; exists {
					line = regexp.MustCompile(`OPType: \w+`).ReplaceAllString(line, "OPType: Update")
					_, err = outputTrace.WriteString(line)
					lineChangedToUpdate++
					if err != nil {
						fmt.Println("Error writing to output trace file:", err)
					}
				} else {
					_, err := outputTrace.WriteString(line)
					if err != nil {
						fmt.Println("Error writing to output trace file:", err)
					}
					keySet[string(keyBytes)] = struct{}{}
					lineNotChangedToUpdate++
				}
				// close the closer
				if closer != nil {
					closer.Close()
				}
			} else {
				// Key found, change the opType to "Update" and write the original line (with Update opType) to the output trace file
				line = regexp.MustCompile(`OPType: \w+`).ReplaceAllString(line, "OPType: Update")
				_, err = outputTrace.WriteString(line)
				lineChangedToUpdate++
				if err != nil {
					fmt.Println("Error writing to output trace file:", err)
				}
				// close the closer
				if closer != nil {
					closer.Close()
				}
			}
		} else {
			// for other opTypes, write the line to the output trace file
			_, err := outputTrace.WriteString(line)
			if err != nil {
				fmt.Println("Error writing to output trace file:", err)
			}
		}
	}
	// print the number of lines changed to Update
	fmt.Printf("\nNumber of lines changed to Update: %d\n", lineChangedToUpdate)
}
