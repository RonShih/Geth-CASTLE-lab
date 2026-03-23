package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PrefixCategory maps a hex-encoded prefix to a human-readable category name.
type PrefixCategory struct {
	Prefix   string
	Category string
}

// hexPrefixes is copied from analysisOpDistributionByBatch.go.
var hexPrefixes = []PrefixCategory{
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

func matchPrefix(key string) string {
	for _, p := range hexPrefixes {
		if strings.HasPrefix(key, p.Prefix) {
			return p.Category
		}
	}
	return "Unknown"
}

// StackStats holds statistics for a single call stack pattern within a category.
type StackStats struct {
	Count        int
	LevelDist    map[int]int
	CrossSSTable int
	SameSSTable  int
	CrossLevel   int
}

func newStackStats() *StackStats {
	return &StackStats{LevelDist: make(map[int]int)}
}

// CategoryStats holds all statistics for a single KV prefix category.
type CategoryStats struct {
	OpCounts       map[string]int          // op type -> total count
	LevelDist      map[int]int             // level -> Get count (-1 = not found/memtable)
	CrossSSTable   int                     // Gets where sstable id differs from the previous Get
	SameSSTable    int                     // Gets where sstable id equals the previous Get
	CrossLevel     int                     // Gets where level differs from the previous Get
	GetTotal       int                     // total Get operation count
	StackBreakdown map[string]*StackStats  // simplified stack pattern -> stats
}

func newCategoryStats() *CategoryStats {
	return &CategoryStats{
		OpCounts:       make(map[string]int),
		LevelDist:      make(map[int]int),
		StackBreakdown: make(map[string]*StackStats),
	}
}

// simplifyStack deduplicates consecutive repeated frames.
// e.g. "a.go:1>b.go:2>b.go:2>b.go:2>c.go:3" → "a.go:1>b.go:2(x3)>c.go:3"
func simplifyStack(raw string) string {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ">")
	var result []string
	for i := 0; i < len(parts); {
		j := i + 1
		for j < len(parts) && parts[j] == parts[i] {
			j++
		}
		count := j - i
		if count > 1 {
			result = append(result, fmt.Sprintf("%s(x%d)", parts[i], count))
		} else {
			result = append(result, parts[i])
		}
		i = j
	}
	return strings.Join(result, ">")
}

func getOrCreate(m map[string]*CategoryStats, cat string) *CategoryStats {
	if m[cat] == nil {
		m[cat] = newCategoryStats()
	}
	return m[cat]
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: analysisReadAmplification <trace_file> [output_dir]")
		fmt.Fprintln(os.Stderr, "  trace_file : path to blktrace log file")
		fmt.Fprintln(os.Stderr, "  output_dir : directory for output files (default: ./raOutput)")
		os.Exit(1)
	}
	traceFile := os.Args[1]
	outputDir := "./raOutput"
	if len(os.Args) >= 3 {
		outputDir = os.Args[2]
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output dir %q: %v\n", outputDir, err)
		os.Exit(1)
	}

	// Compiled regexes.
	// getRegex matches a Get line that includes level, sstable, and optional stack fields.
	getRegex := regexp.MustCompile(
		`OPType: Get, key: ([a-fA-F0-9]+), size: \d+, level: (-?\d+), sstable: (\d+)(?:, gid: \d+, stack: (.+))?`)
	// opRegex matches any other operation line (Put, BatchPut, Delete, etc.).
	opRegex := regexp.MustCompile(
		`OPType: (\w+(?: \w+)*), (?:key: ([a-fA-F0-9]+)|prefix: ([a-fA-F0-9]+))?`)

	allStats := make(map[string]*CategoryStats)
	// transMatrix[fromCat][toCat] = number of cross-SSTable transitions
	transMatrix := make(map[string]map[string]int)

	const sentinel = -9999
	lastLevel := sentinel
	lastSSTable := sentinel
	lastCategory := ""

	f, err := os.Open(traceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open trace file %q: %v\n", traceFile, err)
		os.Exit(1)
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 4*1024*1024) // 4 MB read buffer
	var lineCount int64
	start := time.Now()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				break
			}
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "\nError reading file: %v\n", err)
				break
			}
			// Last line without trailing newline — still process it.
		}

		lineCount++
		if lineCount%5_000_000 == 0 {
			fmt.Printf("\rProcessed %d M lines, elapsed: %.1fs ...",
				lineCount/1_000_000, time.Since(start).Seconds())
		}

		// --- Try matching a Get operation (has level + sstable) ---
		if m := getRegex.FindStringSubmatch(line); m != nil {
			key := m[1]
			level, _ := strconv.Atoi(m[2])
			sstable, _ := strconv.Atoi(m[3])
			cat := matchPrefix(key)

			s := getOrCreate(allStats, cat)
			s.GetTotal++
			s.OpCounts["Get"]++
			s.LevelDist[level]++

			// Track per-stack stats if stack is present
			stackKey := "(no stack)"
			if m[4] != "" {
				stackKey = simplifyStack(m[4])
			}
			ss := s.StackBreakdown[stackKey]
			if ss == nil {
				ss = newStackStats()
				s.StackBreakdown[stackKey] = ss
			}
			ss.Count++
			ss.LevelDist[level]++

			if lastLevel != sentinel {
				if sstable != lastSSTable {
					s.CrossSSTable++
					ss.CrossSSTable++
					// Record in transition matrix (from prev category -> this category).
					if transMatrix[lastCategory] == nil {
						transMatrix[lastCategory] = make(map[string]int)
					}
					transMatrix[lastCategory][cat]++
				} else {
					s.SameSSTable++
					ss.SameSSTable++
				}
				if level != lastLevel {
					s.CrossLevel++
					ss.CrossLevel++
				}
			}

			lastLevel = level
			lastSSTable = sstable
			lastCategory = cat
			continue
		}

		// --- Try matching other operations ---
		if m := opRegex.FindStringSubmatch(line); m != nil {
			opType := m[1]
			if opType == "Get" {
				// A Get line that didn't match getRegex (missing level/sstable).
				// Count it but don't update transition state.
				var key string
				if m[2] != "" {
					key = m[2]
				} else if m[3] != "" {
					key = m[3]
				}
				cat := "noPrefix"
				if key != "" {
					cat = matchPrefix(key)
				}
				s := getOrCreate(allStats, cat)
				s.GetTotal++
				s.OpCounts["Get"]++
				continue
			}

			var key string
			if m[2] != "" {
				key = m[2]
			} else if m[3] != "" {
				key = m[3]
			}
			cat := "noPrefix"
			if key != "" {
				cat = matchPrefix(key)
			}
			getOrCreate(allStats, cat).OpCounts[opType]++
		}
	}

	fmt.Printf("\nTotal lines processed: %d, elapsed: %.1fs\n",
		lineCount, time.Since(start).Seconds())

	writeSummary(outputDir, allStats)
	writeTransitionMatrix(outputDir, transMatrix, allStats)
	fmt.Println("Done! Results written to", outputDir)
}

// ---- Output helpers ----

func totalOps(s *CategoryStats) int {
	n := 0
	for _, v := range s.OpCounts {
		n += v
	}
	return n
}

type catEntry struct {
	name  string
	stats *CategoryStats
}

func sortedEntries(allStats map[string]*CategoryStats) []catEntry {
	entries := make([]catEntry, 0, len(allStats))
	for name, s := range allStats {
		entries = append(entries, catEntry{name, s})
	}
	return entries
}

func writeSummary(outputDir string, allStats map[string]*CategoryStats) {
	path := outputDir + "/ra_summary.txt"
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", path, err)
		return
	}
	defer f.Close()

	entries := sortedEntries(allStats)

	// ---- Section 0: Global Summary ----
	var globalGets, globalCross, globalSame, globalCrossLevel int
	for _, e := range entries {
		s := e.stats
		globalGets += s.GetTotal
		globalCross += s.CrossSSTable
		globalSame += s.SameSSTable
		globalCrossLevel += s.CrossLevel
	}
	globalCompared := globalCross + globalSame

	fmt.Fprintln(f, "=== Global Summary ===")
	fmt.Fprintf(f, "  Total Gets (all categories): %d\n", globalGets)
	if globalCompared > 0 {
		fmt.Fprintf(f, "  Cross-SSTable: %12d  (%6.2f%%)\n", globalCross, 100.0*float64(globalCross)/float64(globalCompared))
		fmt.Fprintf(f, "  Same-SSTable:  %12d  (%6.2f%%)\n", globalSame, 100.0*float64(globalSame)/float64(globalCompared))
		fmt.Fprintf(f, "  Cross-Level:   %12d  (%6.2f%%)\n", globalCrossLevel, 100.0*float64(globalCrossLevel)/float64(globalCompared))
	}

	// Top 5 by Gets
	sort.Slice(entries, func(i, j int) bool { return entries[i].stats.GetTotal > entries[j].stats.GetTotal })
	fmt.Fprintln(f, "\n  Top 5 by Gets:")
	for i, e := range entries {
		if i >= 5 || e.stats.GetTotal == 0 {
			break
		}
		pct := 100.0 * float64(e.stats.GetTotal) / float64(globalGets)
		fmt.Fprintf(f, "    %d. %-30s %12d  (%6.2f%% of all Gets)\n", i+1, e.name, e.stats.GetTotal, pct)
	}

	// Top 5 by Cross-SSTable
	sort.Slice(entries, func(i, j int) bool { return entries[i].stats.CrossSSTable > entries[j].stats.CrossSSTable })
	fmt.Fprintln(f, "\n  Top 5 by Cross-SSTable:")
	for i, e := range entries {
		if i >= 5 || e.stats.CrossSSTable == 0 {
			break
		}
		compared := e.stats.CrossSSTable + e.stats.SameSSTable
		pct := 100.0 * float64(e.stats.CrossSSTable) / float64(compared)
		pctOfGlobal := 100.0 * float64(e.stats.CrossSSTable) / float64(globalCross)
		fmt.Fprintf(f, "    %d. %-30s %12d  (%6.2f%% of its Gets, %6.2f%% of global cross)\n", i+1, e.name, e.stats.CrossSSTable, pct, pctOfGlobal)
	}

	// Top 5 by Same-SSTable
	sort.Slice(entries, func(i, j int) bool { return entries[i].stats.SameSSTable > entries[j].stats.SameSSTable })
	fmt.Fprintln(f, "\n  Top 5 by Same-SSTable:")
	for i, e := range entries {
		if i >= 5 || e.stats.SameSSTable == 0 {
			break
		}
		compared := e.stats.CrossSSTable + e.stats.SameSSTable
		pct := 100.0 * float64(e.stats.SameSSTable) / float64(compared)
		pctOfGlobal := 100.0 * float64(e.stats.SameSSTable) / float64(globalSame)
		fmt.Fprintf(f, "    %d. %-30s %12d  (%6.2f%% of its Gets, %6.2f%% of global same)\n", i+1, e.name, e.stats.SameSSTable, pct, pctOfGlobal)
	}
	fmt.Fprintln(f)

	// ---- Section 1: KV Operation Count Summary ----
	sort.Slice(entries, func(i, j int) bool {
		return totalOps(entries[i].stats) > totalOps(entries[j].stats)
	})
	fmt.Fprintln(f, "=== KV Operation Count Summary ===")
	for _, e := range entries {
		fmt.Fprintf(f, "\nCategory: %s  (Total ops: %d)\n", e.name, totalOps(e.stats))

		type opEntry struct {
			op  string
			cnt int
		}
		ops := make([]opEntry, 0, len(e.stats.OpCounts))
		for op, cnt := range e.stats.OpCounts {
			ops = append(ops, opEntry{op, cnt})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].cnt > ops[j].cnt })
		for _, op := range ops {
			fmt.Fprintf(f, "  %-25s %d\n", op.op+":", op.cnt)
		}
	}

	// ---- Section 2: Get Level Distribution ----
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.GetTotal > entries[j].stats.GetTotal
	})
	fmt.Fprintln(f, "\n\n=== Get Level Distribution ===")
	for _, e := range entries {
		s := e.stats
		if s.GetTotal == 0 {
			continue
		}
		fmt.Fprintf(f, "\nCategory: %s  (Total Gets: %d)\n", e.name, s.GetTotal)

		levels := make([]int, 0, len(s.LevelDist))
		for lvl := range s.LevelDist {
			levels = append(levels, lvl)
		}
		sort.Ints(levels)
		for _, lvl := range levels {
			cnt := s.LevelDist[lvl]
			pct := 100.0 * float64(cnt) / float64(s.GetTotal)
			lvlName := fmt.Sprintf("L%d", lvl)
			if lvl == -1 {
				lvlName = "L-1(notfound)"
			}
			fmt.Fprintf(f, "  %-18s %12d  (%6.2f%%)\n", lvlName+":", cnt, pct)
		}
	}

	// ---- Section 3: Read Amplification ----
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.CrossSSTable > entries[j].stats.CrossSSTable
	})
	fmt.Fprintln(f, "\n\n=== Read Amplification: Cross-SSTable/Level Transitions ===")
	fmt.Fprintln(f, "(Consecutive Gets globally; transitions attributed to the destination Get)")
	fmt.Fprintln(f, "(sstable=0 means found in memtable; level=-1 means not found)")
	fmt.Fprintln(f)

	for _, e := range entries {
		s := e.stats
		if s.GetTotal == 0 {
			continue
		}
		compared := s.CrossSSTable + s.SameSSTable
		var crossSSTablPct, sameSSTablPct, crossLevelPct float64
		if compared > 0 {
			crossSSTablPct = 100.0 * float64(s.CrossSSTable) / float64(compared)
			sameSSTablPct = 100.0 * float64(s.SameSSTable) / float64(compared)
			crossLevelPct = 100.0 * float64(s.CrossLevel) / float64(compared)
		}
		getPct := 0.0
		if globalGets > 0 {
			getPct = 100.0 * float64(s.GetTotal) / float64(globalGets)
		}
		fmt.Fprintf(f, "Category: %s  (Total Gets: %d, %.2f%% of all Gets, Compared: %d)\n",
			e.name, s.GetTotal, getPct, compared)
		fmt.Fprintf(f, "  Cross-SSTable (incoming): %12d  (%6.2f%%)\n",
			s.CrossSSTable, crossSSTablPct)
		fmt.Fprintf(f, "  Same-SSTable  (incoming): %12d  (%6.2f%%)\n",
			s.SameSSTable, sameSSTablPct)
		fmt.Fprintf(f, "  Cross-Level   (incoming): %12d  (%6.2f%%)\n\n",
			s.CrossLevel, crossLevelPct)
	}

	// ---- Section 4: Get Call Stack Breakdown ----
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stats.GetTotal > entries[j].stats.GetTotal
	})
	fmt.Fprintln(f, "\n\n=== Get Call Stack Breakdown ===")
	fmt.Fprintln(f, "(Per-category breakdown by call stack pattern)")
	fmt.Fprintln(f)

	for _, e := range entries {
		s := e.stats
		if s.GetTotal == 0 || len(s.StackBreakdown) == 0 {
			continue
		}
		fmt.Fprintf(f, "Category: %s  (Total Gets: %d)\n", e.name, s.GetTotal)

		// Sort stacks by count descending
		type stackEntry struct {
			stack string
			stats *StackStats
		}
		stacks := make([]stackEntry, 0, len(s.StackBreakdown))
		for stack, ss := range s.StackBreakdown {
			stacks = append(stacks, stackEntry{stack, ss})
		}
		sort.Slice(stacks, func(i, j int) bool { return stacks[i].stats.Count > stacks[j].stats.Count })

		for _, se := range stacks {
			ss := se.stats
			pct := 100.0 * float64(ss.Count) / float64(s.GetTotal)
			fmt.Fprintf(f, "\n  Stack: %s\n", se.stack)
			fmt.Fprintf(f, "    Gets: %d  (%6.2f%%)\n", ss.Count, pct)

			// Level distribution in one line
			levels := make([]int, 0, len(ss.LevelDist))
			for lvl := range ss.LevelDist {
				levels = append(levels, lvl)
			}
			sort.Ints(levels)
			var lvlParts []string
			for _, lvl := range levels {
				cnt := ss.LevelDist[lvl]
				lvlPct := 100.0 * float64(cnt) / float64(ss.Count)
				lvlName := fmt.Sprintf("L%d", lvl)
				if lvl == -1 {
					lvlName = "L-1(notfound)"
				}
				lvlParts = append(lvlParts, fmt.Sprintf("%s: %.2f%%", lvlName, lvlPct))
			}
			fmt.Fprintf(f, "    Level dist: %s\n", strings.Join(lvlParts, ", "))

			// Cross-SSTable
			compared := ss.CrossSSTable + ss.SameSSTable
			if compared > 0 {
				crossPct := 100.0 * float64(ss.CrossSSTable) / float64(compared)
				fmt.Fprintf(f, "    Cross-SSTable: %d / %d  (%.2f%%)\n", ss.CrossSSTable, compared, crossPct)
			}
		}
		fmt.Fprintln(f)
	}

	fmt.Printf("Summary written to %s\n", path)
}

func writeTransitionMatrix(outputDir string, transMatrix map[string]map[string]int, allStats map[string]*CategoryStats) {
	path := outputDir + "/ra_transition_matrix.csv"
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", path, err)
		return
	}
	defer f.Close()

	// Collect all categories that appear in the transition matrix or have Gets.
	catSet := make(map[string]bool)
	for from, toMap := range transMatrix {
		catSet[from] = true
		for to := range toMap {
			catSet[to] = true
		}
	}
	for cat, s := range allStats {
		if s.GetTotal > 0 {
			catSet[cat] = true
		}
	}

	categories := make([]string, 0, len(catSet))
	for cat := range catSet {
		categories = append(categories, cat)
	}

	// Sort by total incoming cross-SSTable transitions (descending).
	incomingTotal := make(map[string]int)
	for _, toMap := range transMatrix {
		for to, cnt := range toMap {
			incomingTotal[to] += cnt
		}
	}
	sort.Slice(categories, func(i, j int) bool {
		return incomingTotal[categories[i]] > incomingTotal[categories[j]]
	})

	w := csv.NewWriter(f)

	// Header
	header := make([]string, 1+len(categories))
	header[0] = "from\\to"
	copy(header[1:], categories)
	_ = w.Write(header)

	// Rows
	for _, from := range categories {
		row := make([]string, 1+len(categories))
		row[0] = from
		for j, to := range categories {
			cnt := 0
			if transMatrix[from] != nil {
				cnt = transMatrix[from][to]
			}
			row[j+1] = strconv.Itoa(cnt)
		}
		_ = w.Write(row)
	}
	w.Flush()

	fmt.Printf("Transition matrix written to %s\n", path)
}
