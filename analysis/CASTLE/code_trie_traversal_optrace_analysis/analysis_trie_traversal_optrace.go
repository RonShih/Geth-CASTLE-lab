// analysis_trie_traversal_optrace.go
//
// Stage 2 of the trie-traversal analysis pipeline.
//
//   Stage 1 (already exists in code_optrace_analysis/):
//     optrace_<from>_<to>_<datetime>  → trie_traversal_optrace.csv
//
//   Stage 2 (this program):
//     trie_traversal_optrace.csv      → read_ops_analyzed.csv
//
//   Stage 3 (Python in code_trie_traversal_optrace_analysis/):
//     read_ops_analyzed.csv           → report.md + images/*.png
//
// One row per read_op in the output. All per-op aggregates the Python plot layer
// needs are computed here in Go; per-get details that some tasks still require
// (e.g. position×level crosstab, hit/miss sequence drill-down) are emitted as
// comma-delimited sequence strings packed into per-op cells.
//
// Streaming design: input is sorted by read_op_id (the upstream writer guarantees
// this), so we accumulate events for the current op and flush when read_op_id
// changes. Memory is O(events per op); the biggest op (read_op 110049, 1609
// events) stays well under 1 MB.
//
// Usage:
//   go run analysis_trie_traversal_optrace.go INPUT_CSV OUTPUT_CSV
package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Column indices in trie_traversal_optrace.csv (21 columns).
const (
	colReadOpID = iota
	colOpType
	colEventType
	colBlockID
	colGID
	colGoroutineType
	colTravID
	colCategory
	colAccountHash
	colPosition
	colProbeIdx
	colKey
	colLevel
	colSSTable
	colFilterPresent
	colFilterCacheHit
	colFilterPositive
	colIndexCacheHit
	colFound
	colDataCacheHit
	colLatencyNs
)

type getEvent struct {
	travID         int64
	position       int64
	category       string
	gid            string
	goroutineType  string
	key            string
	level          int64
	sstable        int64
	filterCacheHit string
	filterPositive string
	indexCacheHit  string
	found          string
	dataCacheHit   string
	latencyNs      int64
}

type probeEvent struct {
	travID         int64
	position       int64
	probeIdx       int64
	level          int64
	sstable        int64
	filterCacheHit string
	filterPositive string
	indexCacheHit  string
	found          string
	latencyNs      int64
}

type opState struct {
	readOpID       int64
	opType         string
	blockID        string
	accountHash    string
	firstGID       string
	goroutineTypes map[string]struct{}
	gets           []getEvent
	probes         []probeEvent
	getTotalNs     int64
	probeTotalNs   int64
}

func (s *opState) addGet(e getEvent) {
	s.gets = append(s.gets, e)
	s.getTotalNs += e.latencyNs
}

func (s *opState) addProbe(e probeEvent) {
	s.probes = append(s.probes, e)
	s.probeTotalNs += e.latencyNs
}

func (s *opState) bucket() string {
	if s.opType == "account_read" {
		return "account"
	}
	return "storage"
}

// flush computes every per-op aggregate + packed sequence in one pass.
func (s *opState) flush() []string {
	// Sort gets by (trav_id, position) so the packed sequences match the trie
	// walk order. (Input is already sorted that way globally, but be defensive
	// against any out-of-order rows.)
	sort.Slice(s.gets, func(i, j int) bool {
		if s.gets[i].travID != s.gets[j].travID {
			return s.gets[i].travID < s.gets[j].travID
		}
		return s.gets[i].position < s.gets[j].position
	})

	n := len(s.gets)

	// --- aggregates (single pass through events) ---
	// level_switches / sst_switches: count of positions where the current
	// event's level/sstable differs from the previous event's. Repeats on the
	// same level/sst don't count. Trav boundaries DO count (sequence is
	// concatenated across travs sorted by trav_id then position).
	travMaxPos := map[int64]int64{}
	accountTrieDepth := int64(-1)
	nMiss, nClassified := 0, 0
	nDec, nInc := 0, 0
	nLevelSwitches, nSstSwitches := 0, 0

	prevTravID, prevLevel := int64(-1), int64(-1)
	prevSeqLevel, prevSeqSst := int64(-1), int64(-1)
	for i, e := range s.gets {
		// Events are sorted by (trav_id, position) ascending, so the last
		// position seen for a trav is its max.
		travMaxPos[e.travID] = e.position
		if e.category == "TrieAccount" && e.position > accountTrieDepth {
			accountTrieDepth = e.position
		}
		hl := strings.ToLower(e.dataCacheHit)
		if hl == "true" || hl == "false" {
			nClassified++
			if hl == "false" {
				nMiss++
			}
		}
		// Within-trav decreases/increases (zigzag along one descent)
		if e.travID == prevTravID && prevLevel >= 0 {
			d := e.level - prevLevel
			if d < 0 {
				nDec++
			} else if d > 0 {
				nInc++
			}
		}
		prevTravID, prevLevel = e.travID, e.level
		// Sequence-level switches (across the WHOLE op, including trav boundaries)
		if i > 0 {
			if e.level != prevSeqLevel {
				nLevelSwitches++
			}
			if e.sstable != prevSeqSst {
				nSstSwitches++
			}
		}
		prevSeqLevel, prevSeqSst = e.level, e.sstable
	}

	var depthSum, depthMax int64
	for _, p := range travMaxPos {
		depthSum += p
		if p > depthMax {
			depthMax = p
		}
	}
	depthMean := 0.0
	if len(travMaxPos) > 0 {
		depthMean = float64(depthSum) / float64(len(travMaxPos))
	}

	dataMissRate := 0.0
	if nClassified > 0 {
		dataMissRate = float64(nMiss) / float64(nClassified) * 100.0
	}

	// --- packed get sequence strings ---
	var (
		levelSeq, hitSeq, sstSeq, posSeq    strings.Builder
		travSeq, catSeq, latSeq             strings.Builder
		filterCacheSeq, filterPosSeq        strings.Builder
		indexCacheSeq, foundSeq             strings.Builder
	)
	for i, e := range s.gets {
		if i > 0 {
			for _, b := range []*strings.Builder{
				&levelSeq, &hitSeq, &sstSeq, &posSeq, &travSeq, &catSeq, &latSeq,
				&filterCacheSeq, &filterPosSeq, &indexCacheSeq, &foundSeq,
			} {
				b.WriteByte(',')
			}
		}
		levelSeq.WriteString(strconv.FormatInt(e.level, 10))
		hitSeq.WriteString(strings.ToLower(e.dataCacheHit))
		sstSeq.WriteString(strconv.FormatInt(e.sstable, 10))
		posSeq.WriteString(strconv.FormatInt(e.position, 10))
		travSeq.WriteString(strconv.FormatInt(e.travID, 10))
		catSeq.WriteString(e.category)
		latSeq.WriteString(strconv.FormatInt(e.latencyNs, 10))
		filterCacheSeq.WriteString(strings.ToLower(e.filterCacheHit))
		filterPosSeq.WriteString(strings.ToLower(e.filterPositive))
		indexCacheSeq.WriteString(strings.ToLower(e.indexCacheHit))
		foundSeq.WriteString(strings.ToLower(e.found))
	}

	// --- packed probe sequence strings ---
	// Sort probes by (trav_id, position, probe_idx) for deterministic ordering.
	sort.Slice(s.probes, func(i, j int) bool {
		if s.probes[i].travID != s.probes[j].travID {
			return s.probes[i].travID < s.probes[j].travID
		}
		if s.probes[i].position != s.probes[j].position {
			return s.probes[i].position < s.probes[j].position
		}
		return s.probes[i].probeIdx < s.probes[j].probeIdx
	})
	var (
		pLevelSeq, pSstSeq, pPosSeq, pTravSeq                 strings.Builder
		pFilterCacheSeq, pFilterPosSeq, pIndexCacheSeq        strings.Builder
		pFoundSeq, pLatSeq                                    strings.Builder
	)
	for i, e := range s.probes {
		if i > 0 {
			for _, b := range []*strings.Builder{
				&pLevelSeq, &pSstSeq, &pPosSeq, &pTravSeq,
				&pFilterCacheSeq, &pFilterPosSeq, &pIndexCacheSeq, &pFoundSeq, &pLatSeq,
			} {
				b.WriteByte(',')
			}
		}
		pLevelSeq.WriteString(strconv.FormatInt(e.level, 10))
		pSstSeq.WriteString(strconv.FormatInt(e.sstable, 10))
		pPosSeq.WriteString(strconv.FormatInt(e.position, 10))
		pTravSeq.WriteString(strconv.FormatInt(e.travID, 10))
		pFilterCacheSeq.WriteString(strings.ToLower(e.filterCacheHit))
		pFilterPosSeq.WriteString(strings.ToLower(e.filterPositive))
		pIndexCacheSeq.WriteString(strings.ToLower(e.indexCacheHit))
		pFoundSeq.WriteString(strings.ToLower(e.found))
		pLatSeq.WriteString(strconv.FormatInt(e.latencyNs, 10))
	}

	accountTrieDepthStr := ""
	if accountTrieDepth >= 0 {
		accountTrieDepthStr = strconv.FormatInt(accountTrieDepth, 10)
	}

	return []string{
		strconv.FormatInt(s.readOpID, 10),
		s.opType,
		s.bucket(),
		s.blockID,
		s.accountHash,
		s.firstGID,
		joinSortedKeys(s.goroutineTypes, ","),
		strconv.FormatInt(int64(n), 10),
		strconv.FormatInt(int64(len(s.probes)), 10),
		strconv.FormatInt(s.getTotalNs, 10),
		strconv.FormatInt(s.probeTotalNs, 10),
		fmt.Sprintf("%.6f", float64(s.getTotalNs)/1e6),
		strconv.FormatInt(int64(len(travMaxPos)), 10),
		fmt.Sprintf("%.4f", depthMean),
		strconv.FormatInt(depthMax, 10),
		accountTrieDepthStr,
		strconv.FormatInt(int64(nLevelSwitches), 10),
		strconv.FormatInt(int64(nSstSwitches), 10),
		fmt.Sprintf("%.4f", dataMissRate),
		strconv.FormatInt(int64(nDec), 10),
		strconv.FormatInt(int64(nInc), 10),
		levelSeq.String(),
		hitSeq.String(),
		sstSeq.String(),
		posSeq.String(),
		travSeq.String(),
		catSeq.String(),
		latSeq.String(),
		filterCacheSeq.String(),
		filterPosSeq.String(),
		indexCacheSeq.String(),
		foundSeq.String(),
		pLevelSeq.String(),
		pSstSeq.String(),
		pPosSeq.String(),
		pTravSeq.String(),
		pFilterCacheSeq.String(),
		pFilterPosSeq.String(),
		pIndexCacheSeq.String(),
		pFoundSeq.String(),
		pLatSeq.String(),
	}
}

// flushTravs emits one row per trav in this op. Must be called AFTER flush(),
// since flush() sorts s.gets and we rely on that ordering here.
//
// One row in travs_analyzed.csv corresponds to one root→leaf trie walk
// (= one logical key lookup). Within a trav, position increases monotonically;
// across trav boundaries, position resets to 0 and the key starts fresh.
func (s *opState) flushTravs() [][]string {
	if len(s.gets) == 0 {
		return nil
	}
	out := make([][]string, 0, 8)

	// Walk gets in sorted order (already sorted by flush). Detect trav boundaries.
	start := 0
	for i := 1; i <= len(s.gets); i++ {
		// boundary = end of slice OR next event has different trav_id
		if i == len(s.gets) || s.gets[i].travID != s.gets[start].travID {
			out = append(out, s.makeTravRow(s.gets[start:i]))
			start = i
		}
	}
	return out
}

// makeTravRow builds one trav-CSV row from a contiguous slice of gets that
// share the same trav_id (caller guarantees this).
func (s *opState) makeTravRow(travGets []getEvent) []string {
	n := len(travGets)
	first := travGets[0]
	last := travGets[n-1]

	// Aggregates within this trav
	var (
		totalLat, maxLat int64
		nMiss, nHit      int
		levelMin         int64 = first.level
		levelMax         int64 = first.level
		nDec, nInc       int
		sstSet           = map[int64]struct{}{}
	)
	for i, e := range travGets {
		totalLat += e.latencyNs
		if e.latencyNs > maxLat {
			maxLat = e.latencyNs
		}
		hl := strings.ToLower(e.dataCacheHit)
		if hl == "true" {
			nHit++
		} else if hl == "false" {
			nMiss++
		}
		if e.level < levelMin {
			levelMin = e.level
		}
		if e.level > levelMax {
			levelMax = e.level
		}
		if i > 0 {
			d := e.level - travGets[i-1].level
			if d < 0 {
				nDec++
			} else if d > 0 {
				nInc++
			}
		}
		sstSet[e.sstable] = struct{}{}
	}
	leafHit := "false"
	if strings.ToLower(last.dataCacheHit) == "true" {
		leafHit = "true"
	}

	// Pack per-trav sequences
	var (
		levelSeq, hitSeq, sstSeq, posSeq, latSeq strings.Builder
	)
	for i, e := range travGets {
		if i > 0 {
			for _, b := range []*strings.Builder{&levelSeq, &hitSeq, &sstSeq, &posSeq, &latSeq} {
				b.WriteByte(',')
			}
		}
		levelSeq.WriteString(strconv.FormatInt(e.level, 10))
		hitSeq.WriteString(strings.ToLower(e.dataCacheHit))
		sstSeq.WriteString(strconv.FormatInt(e.sstable, 10))
		posSeq.WriteString(strconv.FormatInt(e.position, 10))
		latSeq.WriteString(strconv.FormatInt(e.latencyNs, 10))
	}

	return []string{
		strconv.FormatInt(s.readOpID, 10),     // read_op_id
		strconv.FormatInt(first.travID, 10),   // trav_id
		s.opType,                              // op_type
		first.category,                        // category (TrieAccount | TrieStorage)
		s.blockID,                             // block_id
		s.accountHash,                         // account_hash
		first.gid,                             // gid (this trav's goroutine)
		first.goroutineType,                   // goroutine_type
		strconv.Itoa(n),                       // n_gets (nodes touched along this descent)
		strconv.FormatInt(last.position, 10),  // depth (= max position; positions are 0-indexed)
		strconv.FormatInt(first.level, 10),    // start_level (level of the first get / trie root)
		strconv.FormatInt(last.level, 10),     // end_level (level of the last get / trie leaf)
		strconv.FormatInt(levelMin, 10),       // level_min within trav
		strconv.FormatInt(levelMax, 10),       // level_max within trav
		strconv.Itoa(nDec),                    // n_within_trav_decreases
		strconv.Itoa(nInc),                    // n_within_trav_increases
		strconv.Itoa(nHit),                    // n_data_hit
		strconv.Itoa(nMiss),                   // n_data_miss
		leafHit,                               // leaf_hit (whether the last get is data_cache_hit=true)
		strconv.Itoa(len(sstSet)),             // distinct_ssts
		strconv.FormatInt(totalLat, 10),       // total_latency_ns (sum of get latencies for this trav)
		strconv.FormatInt(maxLat, 10),         // max_latency_ns (slowest single get)
		levelSeq.String(),                     // level_seq
		hitSeq.String(),                       // data_hit_seq
		sstSeq.String(),                       // sst_seq
		posSeq.String(),                       // position_seq
		latSeq.String(),                       // latency_ns_seq
		last.key,                              // leaf_key (key of the deepest get; identifies what was looked up)
	}
}

func joinSortedKeys(m map[string]struct{}, sep string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, sep)
}

// outHeader = one row per read_op (=read_ops_analyzed.csv).
// Per-op aggregates + ALL packed get/probe sequences for that op.
var outHeader = []string{
	"read_op_id", "op_type", "bucket", "block_id", "account_hash",
	"first_gid", "goroutine_types",
	"get_count", "probe_count", "get_total_ns", "probe_total_ns", "latency_ms",
	"n_leaves", "trie_depth_mean", "trie_depth_max", "account_trie_depth",
	"level_switches", "sst_switches", "data_miss_rate",
	"n_within_trav_decreases", "n_within_trav_increases",
	"level_seq", "data_hit_seq", "sst_seq", "position_seq",
	"trav_id_seq", "category_seq", "latency_ns_seq",
	"filter_cache_hit_seq", "filter_positive_seq",
	"index_cache_hit_seq", "found_seq",
	"probe_level_seq", "probe_sst_seq", "probe_position_seq", "probe_trav_id_seq",
	"probe_filter_cache_hit_seq", "probe_filter_positive_seq", "probe_index_cache_hit_seq",
	"probe_found_seq", "probe_latency_ns_seq",
}

// travHeader = one row per trav (=travs_analyzed.csv).
// A trav is one root→leaf trie walk by one goroutine for one key.
var travHeader = []string{
	"read_op_id", "trav_id", "op_type", "category", "block_id", "account_hash",
	"gid", "goroutine_type",
	"n_gets", "depth", "start_level", "end_level", "level_min", "level_max",
	"n_within_trav_decreases", "n_within_trav_increases",
	"n_data_hit", "n_data_miss", "leaf_hit", "distinct_ssts",
	"total_latency_ns", "max_latency_ns",
	"level_seq", "data_hit_seq", "sst_seq", "position_seq", "latency_ns_seq",
	"leaf_key",
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: analysis_trie_traversal_optrace INPUT_CSV OPS_OUTPUT_CSV")
		fmt.Fprintln(os.Stderr, "  Produces two CSVs in the same directory as OPS_OUTPUT_CSV:")
		fmt.Fprintln(os.Stderr, "    - OPS_OUTPUT_CSV         (one row per read_op)")
		fmt.Fprintln(os.Stderr, "    - travs_analyzed.csv     (one row per trav, same dir)")
		os.Exit(1)
	}
	inPath, opsOutPath := os.Args[1], os.Args[2]
	travsOutPath := filepath.Join(filepath.Dir(opsOutPath), "travs_analyzed.csv")

	inFile, err := os.Open(inPath)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	defer inFile.Close()
	reader := csv.NewReader(inFile)
	reader.FieldsPerRecord = 21
	reader.ReuseRecord = true

	header, err := reader.Read()
	if err != nil {
		log.Fatalf("read header: %v", err)
	}
	if len(header) != 21 {
		log.Fatalf("expected 21 input columns, got %d", len(header))
	}

	// Per-op CSV
	opsFile, err := os.Create(opsOutPath)
	if err != nil {
		log.Fatalf("create ops output: %v", err)
	}
	defer opsFile.Close()
	opsWriter := csv.NewWriter(opsFile)
	defer opsWriter.Flush()
	if err := opsWriter.Write(outHeader); err != nil {
		log.Fatalf("write ops header: %v", err)
	}

	// Per-trav CSV (same directory)
	travsFile, err := os.Create(travsOutPath)
	if err != nil {
		log.Fatalf("create travs output: %v", err)
	}
	defer travsFile.Close()
	travsWriter := csv.NewWriter(travsFile)
	defer travsWriter.Flush()
	if err := travsWriter.Write(travHeader); err != nil {
		log.Fatalf("write travs header: %v", err)
	}

	var current *opState
	rowCount, opCount, travCount := 0, 0, 0

	flushCurrent := func() {
		if current == nil {
			return
		}
		// flush() sorts gets and emits per-op row; flushTravs() then groups
		// the (now-sorted) gets by trav_id and emits per-trav rows.
		if err := opsWriter.Write(current.flush()); err != nil {
			log.Fatalf("write op row %d: %v", opCount, err)
		}
		for _, tr := range current.flushTravs() {
			if err := travsWriter.Write(tr); err != nil {
				log.Fatalf("write trav row %d: %v", travCount, err)
			}
			travCount++
		}
		opCount++
	}

	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("read row %d: %v", rowCount+1, err)
		}
		rowCount++

		readOpID := parseInt64(rec[colReadOpID])

		if current != nil && current.readOpID != readOpID {
			flushCurrent()
			current = nil
		}
		if current == nil {
			current = &opState{
				readOpID:       readOpID,
				opType:         rec[colOpType],
				blockID:        rec[colBlockID],
				accountHash:    rec[colAccountHash],
				firstGID:       rec[colGID],
				goroutineTypes: map[string]struct{}{},
			}
		}

		current.goroutineTypes[rec[colGoroutineType]] = struct{}{}
		if current.accountHash == "" && rec[colAccountHash] != "" {
			current.accountHash = rec[colAccountHash]
		}

		latencyNs := parseInt64(rec[colLatencyNs])

		switch rec[colEventType] {
		case "get":
			current.addGet(getEvent{
				travID:         parseInt64(rec[colTravID]),
				position:       parseInt64(rec[colPosition]),
				category:       rec[colCategory],
				gid:            rec[colGID],
				goroutineType:  rec[colGoroutineType],
				key:            rec[colKey],
				level:          parseInt64(rec[colLevel]),
				sstable:        parseInt64(rec[colSSTable]),
				filterCacheHit: rec[colFilterCacheHit],
				filterPositive: rec[colFilterPositive],
				indexCacheHit:  rec[colIndexCacheHit],
				found:          rec[colFound],
				dataCacheHit:   rec[colDataCacheHit],
				latencyNs:      latencyNs,
			})
		case "probe":
			current.addProbe(probeEvent{
				travID:         parseInt64(rec[colTravID]),
				position:       parseInt64(rec[colPosition]),
				probeIdx:       parseInt64(rec[colProbeIdx]),
				level:          parseInt64(rec[colLevel]),
				sstable:        parseInt64(rec[colSSTable]),
				filterCacheHit: rec[colFilterCacheHit],
				filterPositive: rec[colFilterPositive],
				indexCacheHit:  rec[colIndexCacheHit],
				found:          rec[colFound],
				latencyNs:      latencyNs,
			})
		}
	}
	flushCurrent()

	opsWriter.Flush()
	if err := opsWriter.Error(); err != nil {
		log.Fatalf("ops writer error: %v", err)
	}
	travsWriter.Flush()
	if err := travsWriter.Error(); err != nil {
		log.Fatalf("travs writer error: %v", err)
	}

	fmt.Printf("read %d input rows → wrote %d read_ops (%s) + %d travs (%s)\n",
		rowCount, opCount, opsOutPath, travCount, travsOutPath)
}
