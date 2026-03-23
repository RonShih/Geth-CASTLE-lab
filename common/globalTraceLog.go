package common

import (
	"bytes"
	"fmt"
	syslog "log"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

// Tino: global logger for trace collection
var gethLogger *syslog.Logger
var logFile *os.File
var targetStartBlockNumber uint64 = 11351600 // The start block number for trace collection
var targetEndBlockNumber uint64 = 11351800   // The end block number for trace collection
var shouldGlobalLogInUse bool = false        // Flag to enable or disable global logging, it will be set to true when the target start block number is reached

var logIsInitiated bool = false

// CASTLE: Operation type constants for trie node stats
const (
	TrieOpGet     = 0
	TrieOpInsert  = 1
	TrieOpDelete  = 2
	TrieOpGetNode = 3
	TrieOpCount   = 4
)

// CASTLE: Operation names for CSV output
var TrieOpNames = [TrieOpCount]string{"get", "insert", "delete", "getNode"}

// CASTLE: Per-operation atomic counters [TrieOpCount]
// CASTLE: Resolved: hashNode → DB read → decoded node type + blob size
var TrieResolvedShortCount [TrieOpCount]int64
var TrieResolvedShortBytes [TrieOpCount]int64
var TrieResolvedFullCount [TrieOpCount]int64
var TrieResolvedFullBytes [TrieOpCount]int64

// CASTLE: Traversed: node types encountered during get/insert/delete/getNode recursion
var TrieTraversedShort [TrieOpCount]int64
var TrieTraversedFull [TrieOpCount]int64
var TrieTraversedHash [TrieOpCount]int64
var TrieTraversedValue [TrieOpCount]int64
var TrieTraversedNil [TrieOpCount]int64

// CASTLE: Traversed bytes: actual data accessed per node type during traversal
var TrieTraversedShortBytes [TrieOpCount]int64 // len(n.Key): key bytes compared during prefix matching
var TrieTraversedFullBytes [TrieOpCount]int64  // unsafe.Sizeof(n.Children[key[pos]]): one interface slot (16 bytes)
var TrieTraversedValueBytes [TrieOpCount]int64 // len(n): value bytes returned/examined
var TrieTraversedHashBytes [TrieOpCount]int64  // len(blob): RLP blob size from resolveAndTrack (disk I/O)

// CASTLE: Independent CSV file for trie node stats
var trieStatsFile *os.File
var trieStatsTotalResolvedShort [TrieOpCount]int64
var trieStatsTotalResolvedShortBytes [TrieOpCount]int64
var trieStatsTotalResolvedFull [TrieOpCount]int64
var trieStatsTotalResolvedFullBytes [TrieOpCount]int64
var trieStatsTotalTraversedShort [TrieOpCount]int64
var trieStatsTotalTraversedFull [TrieOpCount]int64
var trieStatsTotalTraversedHash [TrieOpCount]int64
var trieStatsTotalTraversedValue [TrieOpCount]int64
var trieStatsTotalTraversedNil [TrieOpCount]int64
var trieStatsTotalTraversedShortBytes [TrieOpCount]int64
var trieStatsTotalTraversedFullBytes [TrieOpCount]int64
var trieStatsTotalTraversedValueBytes [TrieOpCount]int64
var trieStatsTotalTraversedHashBytes [TrieOpCount]int64

func GetTargetStartBlockNumber() uint64 {
	return targetStartBlockNumber
}

func GetTargetEndBlockNumber() uint64 {
	return targetEndBlockNumber
}

func SetEnableGlobalLog(enable bool) {
	shouldGlobalLogInUse = enable
	if !enable {
		fmt.Println("Global log is disabled.")
	} else {
		fmt.Println("Global log is enabled.")
	}
}

// CASTLE: Check if global logging is enabled
func IsGlobalLogEnabled() bool { return shouldGlobalLogInUse }

func GoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [..."
	field := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	field = field[:bytes.IndexByte(field, ' ')]
	id, _ := strconv.ParseInt(string(field), 10, 64)
	return id
}

func WriteGlobalLog(msg string) {
	if shouldGlobalLogInUse {
		if logIsInitiated && gethLogger != nil {
			gethLogger.Println(msg)
		}
	}
}

func InitGlobalLog() bool {
	currentLogTime := time.Now().Format("2006-01-02-15-04-05")
	currentLogFileName := fmt.Sprintf("./blktrace_%d_%d_%s", targetStartBlockNumber, targetEndBlockNumber, currentLogTime)

	file, err := os.OpenFile(currentLogFileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		fmt.Println("Error opening global log file:", err)
		logIsInitiated = false
		return false
	}
	logFile = file
	gethLogger = syslog.New(file, "geth: ", syslog.Lshortfile|syslog.Ldate|syslog.Ltime)
	fmt.Println("Global log file opened successfully")
	logIsInitiated = true
	WriteGlobalLog("Global log file opened successfully")

	// CASTLE: Open independent CSV file for trie node stats
	csvFileName := fmt.Sprintf("./trie_node_stats_%d_%d_%s.csv", targetStartBlockNumber, targetEndBlockNumber, currentLogTime)
	csvFile, csvErr := os.OpenFile(csvFileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if csvErr != nil {
		fmt.Println("Error opening trie stats CSV file:", csvErr)
	} else {
		trieStatsFile = csvFile
		// CASTLE: Write CSV header
		fmt.Fprintln(trieStatsFile, "block_id,op,traversed_short,traversed_full,traversed_value,traversed_hash,traversed_short_bytes,traversed_full_bytes,traversed_value_bytes,traversed_hash_bytes,resolved_short,resolved_full,resolved_short_bytes,resolved_full_bytes")
		fmt.Println("Trie node stats CSV file opened:", csvFileName)
	}

	return true
}

// CASTLE: FlushTrieNodeStats swaps all atomic counters, writes one CSV row per op, and accumulates totals.
func FlushTrieNodeStats(blockID string) {
	if !shouldGlobalLogInUse || trieStatsFile == nil {
		return
	}
	for op := 0; op < TrieOpCount; op++ {
		ts := atomic.SwapInt64(&TrieTraversedShort[op], 0)
		tf := atomic.SwapInt64(&TrieTraversedFull[op], 0)
		tv := atomic.SwapInt64(&TrieTraversedValue[op], 0)
		th := atomic.SwapInt64(&TrieTraversedHash[op], 0)
		tsb := atomic.SwapInt64(&TrieTraversedShortBytes[op], 0)
		tfb := atomic.SwapInt64(&TrieTraversedFullBytes[op], 0)
		tvb := atomic.SwapInt64(&TrieTraversedValueBytes[op], 0)
		thb := atomic.SwapInt64(&TrieTraversedHashBytes[op], 0)
		rsc := atomic.SwapInt64(&TrieResolvedShortCount[op], 0)
		rfc := atomic.SwapInt64(&TrieResolvedFullCount[op], 0)
		rsb := atomic.SwapInt64(&TrieResolvedShortBytes[op], 0)
		rfb := atomic.SwapInt64(&TrieResolvedFullBytes[op], 0)

		fmt.Fprintf(trieStatsFile, "%s,%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
			blockID, TrieOpNames[op],
			ts, tf, tv, th,
			tsb, tfb, tvb, thb,
			rsc, rfc, rsb, rfb)

		// CASTLE: Accumulate totals
		trieStatsTotalTraversedShort[op] += ts
		trieStatsTotalTraversedFull[op] += tf
		trieStatsTotalTraversedValue[op] += tv
		trieStatsTotalTraversedHash[op] += th
		trieStatsTotalTraversedShortBytes[op] += tsb
		trieStatsTotalTraversedFullBytes[op] += tfb
		trieStatsTotalTraversedValueBytes[op] += tvb
		trieStatsTotalTraversedHashBytes[op] += thb
		trieStatsTotalResolvedShort[op] += rsc
		trieStatsTotalResolvedFull[op] += rfc
		trieStatsTotalResolvedShortBytes[op] += rsb
		trieStatsTotalResolvedFullBytes[op] += rfb
	}
}

func CloseGlobalLog() {
	// CASTLE: Write TOTAL rows (one per op) and close trie stats CSV
	if trieStatsFile != nil {
		for op := 0; op < TrieOpCount; op++ {
			fmt.Fprintf(trieStatsFile, "TOTAL,%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
				TrieOpNames[op],
				trieStatsTotalTraversedShort[op], trieStatsTotalTraversedFull[op],
				trieStatsTotalTraversedValue[op], trieStatsTotalTraversedHash[op],
				trieStatsTotalTraversedShortBytes[op], trieStatsTotalTraversedFullBytes[op],
				trieStatsTotalTraversedValueBytes[op], trieStatsTotalTraversedHashBytes[op],
				trieStatsTotalResolvedShort[op], trieStatsTotalResolvedFull[op],
				trieStatsTotalResolvedShortBytes[op], trieStatsTotalResolvedFullBytes[op])
		}
		// CASTLE: Write a grand TOTAL row summing all operations
		var grandTS, grandTF, grandTV, grandTH int64
		var grandTSB, grandTFB, grandTVB, grandTHB int64
		var grandRSC, grandRFC, grandRSB, grandRFB int64
		for op := 0; op < TrieOpCount; op++ {
			grandTS += trieStatsTotalTraversedShort[op]
			grandTF += trieStatsTotalTraversedFull[op]
			grandTV += trieStatsTotalTraversedValue[op]
			grandTH += trieStatsTotalTraversedHash[op]
			grandTSB += trieStatsTotalTraversedShortBytes[op]
			grandTFB += trieStatsTotalTraversedFullBytes[op]
			grandTVB += trieStatsTotalTraversedValueBytes[op]
			grandTHB += trieStatsTotalTraversedHashBytes[op]
			grandRSC += trieStatsTotalResolvedShort[op]
			grandRFC += trieStatsTotalResolvedFull[op]
			grandRSB += trieStatsTotalResolvedShortBytes[op]
			grandRFB += trieStatsTotalResolvedFullBytes[op]
		}
		fmt.Fprintf(trieStatsFile, "TOTAL,all,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
			grandTS, grandTF, grandTV, grandTH,
			grandTSB, grandTFB, grandTVB, grandTHB,
			grandRSC, grandRFC, grandRSB, grandRFB)

		trieStatsFile.Close()
		fmt.Println("Trie node stats CSV file closed")
	}

	if logFile != nil {
		logFile.Close()
		fmt.Println("Global log file closed")
	}
}

func StopChainManually() {
	pid := os.Getpid()
	fmt.Printf("Current process PID: %d\n", pid)
	err := syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		fmt.Println("Failed to send SIGINT:", err)
		return
	}
	time.Sleep(2 * time.Second)
	fmt.Println("SIGINT sent. Process should be interrupted if it handles SIGINT.")
}
