package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"math"
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

// PrefetcherStats holds per-group (prefetcher vs non-prefetcher) statistics.
type PrefetcherStats struct {
	GetsByCategory       map[string]int
	CrossByCategory      map[string]int
	SameByCategory       map[string]int
	CrossLevelByCategory map[string]int
	LevelDist            map[int]int
}

func newPrefetcherStats() *PrefetcherStats {
	return &PrefetcherStats{
		GetsByCategory:       make(map[string]int),
		CrossByCategory:      make(map[string]int),
		SameByCategory:       make(map[string]int),
		CrossLevelByCategory: make(map[string]int),
		LevelDist:            make(map[int]int),
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

// ---------------------------------------------------------------------------
// Trie traversal grouping (ported from analyzeTrieTraversal.py)
//
// A "traversal" is a sequence of consecutive Get operations on the same
// goroutine (gid) where each subsequent key extends the previous one as a
// prefix. This corresponds to one logical trie/snap lookup walking down the
// tree. Snap reads (depth-1) are also modeled as 1-key traversals.
// ---------------------------------------------------------------------------

// traversalCategory maps a 2-hex-char key prefix to the Py-style category name
// used for traversal grouping. Returns "" if the prefix is not Trie/Snap.
func traversalCategory(prefix string) string {
	switch prefix {
	case "41":
		return "TrieAccount"
	case "4f":
		return "TrieStorage"
	case "61":
		return "SnapAccount"
	case "6f":
		return "SnapStorage"
	}
	return ""
}

// extractAccountHash returns the 64-char account hash embedded in a TrieStorage
// key (prefix 4f). For other categories, returns the key minus its 2-char prefix.
func extractAccountHash(key string) string {
	if len(key) > 66 {
		return key[2:66]
	}
	if len(key) >= 2 {
		return key[2:]
	}
	return ""
}

// nibblePathToHashPrefix converts a TrieAccount leaf key (path-based scheme)
// to the corresponding account-hash prefix. TrieAccount keys are 0x41 followed
// by a nibble path where each nibble occupies one byte (low hex char). This
// pairs consecutive nibble-bytes into hash bytes.
//
// Example: "41050f050e" → strip "41" → path "050f050e" → pair (1,3)+(5,7)
// → "5f5e".
func nibblePathToHashPrefix(leafKey string) string {
	if len(leafKey) < 2 {
		return ""
	}
	path := leafKey[2:]
	var b strings.Builder
	// Mirrors Python's `range(0, len(path)-2, 4)`: iterate while a full
	// nibble-byte pair fits starting at i. Equivalent to `i+2 < len(path)`.
	for i := 0; i+2 < len(path); i += 4 {
		b.WriteByte(path[i+1])
		b.WriteByte(path[i+3])
	}
	if len(path)%4 == 2 {
		b.WriteByte(path[len(path)-1])
	}
	return b.String()
}

// blockOffsetKey is a (sstable, blockOffset) tuple used to identify a single
// Pebble data block.
type blockOffsetKey struct {
	sstable     int
	blockOffset int64
}

// Traversal accumulates Get operations belonging to one trie traversal.
type Traversal struct {
	// ID is a process-unique sequential integer assigned when the traversal
	// is created. Exported as the traversal_id column in both traversal_detail.csv
	// and pebble_probes.csv so the two can be joined in pandas / SQL.
	ID            int64
	BlockID       string
	GoroutineType string // "main_thread" or "prefetcher"
	GID           string
	Category      string // "TrieAccount" / "TrieStorage" / "SnapAccount" / "SnapStorage"
	AccountHash   string // for TrieStorage only

	Keys         []string
	Sstables     []int
	BlockOffsets []blockOffsetKey
	CacheHits    []bool
	Latencies    []int64
	Levels       []int

	// ProbeRows holds the bound probe events per Get. ProbeRows[i] is the list
	// of SSTableProbe events that fired before Keys[i] was finalised by Pebble.
	// Populated by probeRecorder.onGet at trace-parse time so trie_traversal_optrace.csv
	// can emit get+probe events grouped by read_op without a second pass over
	// pebble_probes.csv.
	ProbeRows [][]pendingProbe
}

// activeTraversal holds the in-progress Traversal for a gid plus the last key
// seen, used to decide whether the next Get extends the same traversal.
type activeTraversal struct {
	lastKey string
	trav    *Traversal
}

// pendingProbe is one parsed SSTableProbe event waiting to be bound to the Get
// that triggered it. Probes accumulate in pendingProbes[gid] until the Get
// event for the same gid+key arrives, at which point they are written to
// pebble_probes.csv with the Get's (traversal_id, position).
type pendingProbe struct {
	Key            string
	Level          int
	SSTable        int
	FilterPresent  bool
	FilterCacheHit string // "true" / "false" / "-" (tri-state preserved verbatim)
	FilterPositive string
	IndexCacheHit  string
	DataCacheHit   string
	Found          bool
	LatencyNs      int64
}

// probeRecorder owns the pebble_probes.csv writer plus the per-gid pending
// queue. Methods are not concurrency-safe — the Pass-2 main loop is
// single-threaded by design.
type probeRecorder struct {
	w       *csv.Writer
	pending map[string][]pendingProbe // gid -> probes accumulated since last Get on that gid
	written int64                     // total probe rows written (for sanity check at end)
	dropped int64                     // total probes dropped as orphans (no matching Get)
}

func newProbeRecorder(w *csv.Writer) *probeRecorder {
	return &probeRecorder{w: w, pending: make(map[string][]pendingProbe)}
}

// onProbe queues a probe under its gid. If the gid currently has probes for a
// different key (the previous Get never arrived — usually means the key was
// not in the DB and Geth went to ErrNotFound), the old probes are dropped as
// orphans.
func (pr *probeRecorder) onProbe(gid string, p pendingProbe) {
	cur := pr.pending[gid]
	if len(cur) > 0 && cur[0].Key != p.Key {
		// Different key — previous batch never got its Get event. Drop.
		pr.dropped += int64(len(cur))
		cur = cur[:0]
	}
	pr.pending[gid] = append(cur, p)
}

// onGet binds whatever is pending for (gid, key) to (traversalID, position) and
// emits one CSV row per probe. If the pending queue's key doesn't match the
// Get's key, those probes are orphans (dropped). The buffer is cleared either
// way so a later Get on the same gid starts fresh.
// Returns the slice of bound probes so the caller can attach them to the
// matching Traversal.ProbeRows (used by trie_traversal_optrace.csv).
func (pr *probeRecorder) onGet(gid, key string, traversalID int64, position int) ([]pendingProbe, error) {
	cur := pr.pending[gid]
	delete(pr.pending, gid)

	if traversalID == 0 || len(cur) == 0 || cur[0].Key != key {
		// Not bindable: no traversal (non-Trie/Snap Get), no probes (memtable
		// hit), or key mismatch (orphan).
		pr.dropped += int64(len(cur))
		return nil, nil
	}
	for i, p := range cur {
		row := []string{
			strconv.FormatInt(traversalID, 10),
			strconv.Itoa(position),
			strconv.Itoa(i),
			strconv.Itoa(p.Level),
			strconv.Itoa(p.SSTable),
			strconv.FormatBool(p.FilterPresent),
			p.FilterCacheHit,
			p.FilterPositive,
			p.IndexCacheHit,
			p.DataCacheHit,
			strconv.FormatBool(p.Found),
			strconv.FormatInt(p.LatencyNs, 10),
		}
		if err := pr.w.Write(row); err != nil {
			return nil, err
		}
		pr.written++
	}
	return cur, nil
}

// flushAtEOF reports any probes still in flight at end of trace. They're
// dropped (no matching Get appeared before EOF). Counted as orphans.
func (pr *probeRecorder) flushAtEOF() {
	for _, cur := range pr.pending {
		pr.dropped += int64(len(cur))
	}
	pr.pending = nil
}

// pebbleProbesHeader matches the columns written per probe.
var pebbleProbesHeader = []string{
	"traversal_id", "position", "probe_idx",
	"level", "sstable",
	"filter_present", "filter_cache_hit", "filter_positive",
	"index_cache_hit", "data_cache_hit",
	"found", "latency_ns",
}

// orderedActiveMap is a map[gid]*activeTraversal that also remembers the order
// in which each gid first appeared. EOF flush iterates in that insertion order
// to mirror Python 3.7+ dict ordering used by analyzeTrieTraversal.py.
type orderedActiveMap struct {
	m     map[string]*activeTraversal
	order []string
}

func newOrderedActiveMap() *orderedActiveMap {
	return &orderedActiveMap{m: make(map[string]*activeTraversal)}
}

func (om *orderedActiveMap) get(gid string) (*activeTraversal, bool) {
	at, ok := om.m[gid]
	return at, ok
}

func (om *orderedActiveMap) set(gid string, at *activeTraversal) {
	if _, exists := om.m[gid]; !exists {
		om.order = append(om.order, gid)
	}
	om.m[gid] = at
}

// flush returns the in-flight traversals in the order each gid first appeared.
func (om *orderedActiveMap) flush() []*Traversal {
	out := make([]*Traversal, 0, len(om.order))
	for _, gid := range om.order {
		if at, ok := om.m[gid]; ok {
			out = append(out, at.trav)
		}
	}
	return out
}

// appendOp adds a Get operation to the traversal in-place.
func (t *Traversal) appendOp(sstable int, blockOffset int64, level int, cacheHit bool, latencyNs int64, key string) {
	t.Keys = append(t.Keys, key)
	t.Sstables = append(t.Sstables, sstable)
	t.BlockOffsets = append(t.BlockOffsets, blockOffsetKey{sstable, blockOffset})
	t.CacheHits = append(t.CacheHits, cacheHit)
	t.Latencies = append(t.Latencies, latencyNs)
	t.Levels = append(t.Levels, level)
}

// TraversalMetrics is a flat view of one Traversal suitable for CSV output and
// downstream summary computations.
type TraversalMetrics struct {
	// ID is the join key into pebble_probes.csv. Copied from Traversal.ID.
	ID                      int64
	BlockID                 string
	GoroutineType           string
	GID                     string
	Category                string
	AccountHash             string
	RootKey                 string
	LeafKey                 string
	Depth                   int
	NumUniqueSstables       int
	NumSstableTransitions   int
	NumUniqueDataBlocks     int
	NumDataBlockTransitions int
	CacheHits               int
	CacheMisses             int
	FirstKeyCacheHit        bool
	SubsequentCacheHitRate  float64 // -1 when depth == 1
	TotalLatencyNs          int64
	FirstKeyLatencyNs       int64
	AvgSubsequentLatencyNs  float64
	LevelsAccessed          string
	NumLevelTransitions     int

	// Per-Get sequences within the traversal (root → leaf). Useful for
	// inspecting LSM access patterns: does the root sit deeper than the
	// leaf? Are SSTable hops monotone or scattered? Stored as comma-joined
	// strings so each traversal still occupies one CSV row.
	RootLevel       int    // Levels[0]
	LeafLevel       int    // Levels[depth-1]
	LevelSequence   string // e.g. "5,4,4,6"
	SstableSequence string // e.g. "2073620,2073620,2092865"
}

// computeMetrics produces the flat per-traversal metrics row.
func (t *Traversal) computeMetrics() TraversalMetrics {
	n := len(t.Keys)
	sstTrans, blkTrans, lvlTrans := 0, 0, 0
	for i := 1; i < n; i++ {
		if t.Sstables[i] != t.Sstables[i-1] {
			sstTrans++
		}
		if t.BlockOffsets[i] != t.BlockOffsets[i-1] {
			blkTrans++
		}
		if t.Levels[i] != t.Levels[i-1] {
			lvlTrans++
		}
	}
	hits := 0
	for _, h := range t.CacheHits {
		if h {
			hits++
		}
	}
	misses := n - hits
	subRate := -1.0
	if n > 1 {
		subHits := 0
		for _, h := range t.CacheHits[1:] {
			if h {
				subHits++
			}
		}
		subRate = float64(subHits) / float64(n-1)
	}
	var totalLat int64
	for _, l := range t.Latencies {
		totalLat += l
	}
	avgSubLat := 0.0
	if n > 1 {
		var s int64
		for _, l := range t.Latencies[1:] {
			s += l
		}
		avgSubLat = float64(s) / float64(n-1)
	}
	uniqSst := uniqueIntCount(t.Sstables)
	uniqBlk := uniqueBlockOffsetCount(t.BlockOffsets)

	// levels_accessed: sorted unique levels joined by comma (string form to
	// match Py output, which uses the raw CSV cell as a string).
	uniqLvl := uniqueIntsSorted(t.Levels)
	uniqLvlParts := make([]string, len(uniqLvl))
	for i, v := range uniqLvl {
		uniqLvlParts[i] = strconv.Itoa(v)
	}

	// Ordered per-Get sequences (root → leaf).
	lvlSeq := make([]string, n)
	sstSeq := make([]string, n)
	for i := 0; i < n; i++ {
		lvlSeq[i] = strconv.Itoa(t.Levels[i])
		sstSeq[i] = strconv.Itoa(t.Sstables[i])
	}

	return TraversalMetrics{
		ID:                      t.ID,
		BlockID:                 t.BlockID,
		GoroutineType:           t.GoroutineType,
		GID:                     t.GID,
		Category:                t.Category,
		AccountHash:             t.AccountHash,
		RootKey:                 t.Keys[0],
		LeafKey:                 t.Keys[n-1],
		Depth:                   n,
		NumUniqueSstables:       uniqSst,
		NumSstableTransitions:   sstTrans,
		NumUniqueDataBlocks:     uniqBlk,
		NumDataBlockTransitions: blkTrans,
		CacheHits:               hits,
		CacheMisses:             misses,
		FirstKeyCacheHit:        t.CacheHits[0],
		SubsequentCacheHitRate:  subRate,
		TotalLatencyNs:          totalLat,
		FirstKeyLatencyNs:       t.Latencies[0],
		AvgSubsequentLatencyNs:  avgSubLat,
		LevelsAccessed:          strings.Join(uniqLvlParts, ","),
		NumLevelTransitions:     lvlTrans,
		RootLevel:               t.Levels[0],
		LeafLevel:               t.Levels[n-1],
		LevelSequence:           strings.Join(lvlSeq, ","),
		SstableSequence:         strings.Join(sstSeq, ","),
	}
}

// ---------------------------------------------------------------------------
// Small numeric helpers (avoids pulling in gonum just for mean/median).
// ---------------------------------------------------------------------------

func uniqueIntCount(xs []int) int {
	seen := make(map[int]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

func uniqueIntsSorted(xs []int) []int {
	seen := make(map[int]struct{}, len(xs))
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Ints(out)
	return out
}

func uniqueBlockOffsetCount(xs []blockOffsetKey) int {
	seen := make(map[blockOffsetKey]struct{}, len(xs))
	for _, x := range xs {
		seen[x] = struct{}{}
	}
	return len(seen)
}

func meanInt(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func meanInt64(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s int64
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func meanFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func medianInt(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]int, len(xs))
	copy(cp, xs)
	sort.Ints(cp)
	n := len(cp)
	if n%2 == 1 {
		return float64(cp[n/2])
	}
	return float64(cp[n/2-1]+cp[n/2]) / 2.0
}

func medianInt64(xs []int64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]int64, len(xs))
	copy(cp, xs)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	n := len(cp)
	if n%2 == 1 {
		return float64(cp[n/2])
	}
	return float64(cp[n/2-1]+cp[n/2]) / 2.0
}

func minInt(xs []int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxInt(xs []int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: analysisReadAmplification <trace_file> [output_dir]")
		fmt.Fprintln(os.Stderr, "  trace_file : path to optrace log file")
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
	// getRegex matches a Get line that includes level, sstable, and optional metadata fields.
	// Groups: 1=key, 2=level, 3=sstable, 4=blockOffset, 5=cacheHit, 6=latencyNs, 7=gid, 8=stack
	// Groups 4..7 are emitted as a single optional block (newer trace format), so they
	// either all match together or are all empty.
	getRegex := regexp.MustCompile(
		`OPType: Get, key: ([a-fA-F0-9]+), size: \d+, level: (-?\d+), sstable: (\d+)` +
			`(?:, blockOffset: (\d+), blockLength: \d+, cacheHit: (true|false), latencyNs: (\d+), gid: (\d+))?` +
			`(?:, stack: (.+))?`)
	// opRegex matches any other operation line (Put, BatchPut, Delete, etc.).
	opRegex := regexp.MustCompile(
		`OPType: (\w+(?: \w+)*), (?:key: ([a-fA-F0-9]+)|prefix: ([a-fA-F0-9]+))?`)
	// blockStartRegex tracks "Processing block (start), ID: N" markers so each Get can
	// be associated with the block being executed (needed for end-to-end analysis).
	blockStartRegex := regexp.MustCompile(`Processing block \(start\), ID: (\d+)`)

	// probeRegex matches an OPType: SSTableProbe line emitted by the instrumented
	// Pebble fork. Groups: 1=key, 2=level, 3=sstable, 4=filterPresent, 5=filterChecked,
	// 6=filterCacheHit, 7=filterPositive, 8=indexCacheHit, 9=dataCacheHit, 10=found,
	// 11=latencyNs, 12=gid. The cache-hit and filterPositive fields are tri-state
	// (true/false/-) so the trace can distinguish "did not happen" from a real false.
	probeRegex := regexp.MustCompile(
		`OPType: SSTableProbe, key: ([a-fA-F0-9]+), level: (-?\d+), sstable: (\d+), ` +
			`filterPresent: (true|false), filterChecked: (true|false), ` +
			`filterCacheHit: (true|false|-), filterPositive: (true|false|-), ` +
			`indexCacheHit: (true|false|-), dataCacheHit: (true|false|-), ` +
			`found: (true|false), latencyNs: (\d+), gid: (\d+)`)

	allStats := make(map[string]*CategoryStats)
	// transMatrix[fromCat][toCat] = number of cross-SSTable transitions
	transMatrix := make(map[string]map[string]int)

	const sentinel = -9999
	lastLevel := sentinel
	lastSSTable := sentinel
	lastCategory := ""

	// currentBlock tracks the most recently announced "Processing block (start)" ID so
	// each Get can be associated with the block being executed.
	currentBlock := ""

	// Active per-goroutine traversals (gid -> in-progress traversal). Trie and
	// Snap chains are tracked in independent ordered maps to mirror
	// analyzeTrieTraversal.py's two-pass design: a Snap Get on gid=G does not
	// interrupt an in-progress Trie traversal on the same gid, and vice versa.
	// Insertion-order tracking keeps EOF-flush order byte-identical to Py.
	activeTrieTraversals := newOrderedActiveMap()
	activeSnapTraversals := newOrderedActiveMap()
	finishedTraversals := make([]*Traversal, 0, 1024)

	// Monotonic counter for Traversal.ID. Starts at 1 so 0 unambiguously means
	// "no traversal" (e.g. when the probe recorder receives a Get for a
	// non-Trie/Snap category).
	var nextTraversalID int64 = 0
	allocTraversalID := func() int64 {
		nextTraversalID++
		return nextTraversalID
	}

	// pebble_probes.csv is streamed: probes arrive interleaved with Gets and
	// every probe is bound at Get-time, so writing during Pass 2 keeps memory
	// flat. Buffered via the standard csv.Writer; final Flush happens after
	// the read loop ends.
	probeCSVPath := outputDir + "/pebble_probes.csv"
	probeFile, err := os.Create(probeCSVPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", probeCSVPath, err)
		os.Exit(1)
	}
	defer probeFile.Close()
	probeCSV := csv.NewWriter(probeFile)
	probeCSV.UseCRLF = true
	if err := probeCSV.Write(pebbleProbesHeader); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write probe CSV header: %v\n", err)
		os.Exit(1)
	}
	probes := newProbeRecorder(probeCSV)

	// --- Pass 1: collect prefetcher goroutine IDs ---
	pfRegex := regexp.MustCompile(`PREFETCHER_START gid: (\d+)`)
	pfGIDs := make(map[string]bool)
	{
		pf, err := os.Open(traceFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open trace file for prefetcher scan: %v\n", err)
			os.Exit(1)
		}
		pfReader := bufio.NewReaderSize(pf, 4*1024*1024)
		for {
			line, err := pfReader.ReadString('\n')
			if err != nil {
				if err == io.EOF && line == "" {
					break
				}
				if err != io.EOF {
					break
				}
			}
			if m := pfRegex.FindStringSubmatch(line); m != nil {
				pfGIDs[m[1]] = true
			}
		}
		pf.Close()
	}
	fmt.Printf("Pass 1: %d prefetcher goroutines identified\n", len(pfGIDs))

	// Prefetcher vs non-prefetcher tracking
	pfStats := newPrefetcherStats()
	npfStats := newPrefetcherStats()
	pfLastLevel := sentinel
	pfLastSSTable := sentinel
	npfLastLevel := sentinel
	npfLastSSTable := sentinel

	// --- Pass 2: main analysis ---
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

		// --- Block start marker: update currentBlock for traversal grouping ---
		if bm := blockStartRegex.FindStringSubmatch(line); bm != nil {
			currentBlock = bm[1]
			continue
		}

		// --- SSTableProbe event: queue under its gid until the Get fires ---
		// Matched before the Get regex because the probe line is more specific
		// and won't accidentally match the Get pattern. Probes are queued only;
		// they get written to pebble_probes.csv when the matching Get arrives.
		if p := probeRegex.FindStringSubmatch(line); p != nil {
			lvl, _ := strconv.Atoi(p[2])
			sst, _ := strconv.Atoi(p[3])
			lat, _ := strconv.ParseInt(p[11], 10, 64)
			probes.onProbe(p[12], pendingProbe{
				Key:            p[1],
				Level:          lvl,
				SSTable:        sst,
				FilterPresent:  p[4] == "true",
				FilterCacheHit: p[6],
				FilterPositive: p[7],
				IndexCacheHit:  p[8],
				DataCacheHit:   p[9],
				Found:          p[10] == "true",
				LatencyNs:      lat,
			})
			continue
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
			if m[8] != "" {
				stackKey = simplifyStack(m[8])
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

			// --- Prefetcher vs non-prefetcher tracking ---
			gidStr := m[7] // may be empty for old-format traces

			// --- Trie/Snap traversal grouping ---
			// Only Get lines with full metadata (gid present) and a Trie/Snap
			// prefix participate. Trie (41/4f) and Snap (61/6f) chains are
			// tracked independently per gid — a Get in one domain does not
			// interrupt an in-flight traversal in the other.
			if gidStr != "" && len(key) >= 2 {
				travCat := traversalCategory(key[:2])
				if travCat != "" {
					var activeMap *orderedActiveMap
					if travCat == "TrieAccount" || travCat == "TrieStorage" {
						activeMap = activeTrieTraversals
					} else {
						activeMap = activeSnapTraversals
					}

					blockOffset, _ := strconv.ParseInt(m[4], 10, 64)
					cacheHit := m[5] == "true"
					latencyNs, _ := strconv.ParseInt(m[6], 10, 64)
					goroutineType := "main_thread"
					if pfGIDs[gidStr] {
						goroutineType = "prefetcher"
					}

					// Bind probes at Get-time: we need (traversal_id, position) which
					// is only known after we decide extend-vs-new below.
					var curTravID int64
					var curPosition int

					if at, ok := activeMap.get(gidStr); ok && strings.HasPrefix(key, at.lastKey) {
						// Same gid+domain, key extends previous → continuation.
						at.trav.appendOp(sstable, blockOffset, level, cacheHit, latencyNs, key)
						at.lastKey = key
						curTravID = at.trav.ID
						curPosition = len(at.trav.Keys) - 1
					} else {
						// Either no active traversal in this domain, or key broke the chain.
						if ok {
							finishedTraversals = append(finishedTraversals, at.trav)
						}
						accountHash := ""
						if travCat == "TrieStorage" {
							accountHash = extractAccountHash(key)
						}
						newTrav := &Traversal{
							ID:            allocTraversalID(),
							BlockID:       currentBlock,
							GoroutineType: goroutineType,
							GID:           gidStr,
							Category:      travCat,
							AccountHash:   accountHash,
							Keys:          []string{key},
							Sstables:      []int{sstable},
							BlockOffsets:  []blockOffsetKey{{sstable, blockOffset}},
							CacheHits:     []bool{cacheHit},
							Latencies:     []int64{latencyNs},
							Levels:        []int{level},
						}
						activeMap.set(gidStr, &activeTraversal{lastKey: key, trav: newTrav})
						curTravID = newTrav.ID
						curPosition = 0
					}
					// Bind queued probes for this gid+key to (traversal_id, position).
					boundProbes, err := probes.onGet(gidStr, key, curTravID, curPosition)
					if err != nil {
						fmt.Fprintf(os.Stderr, "probe csv write: %v\n", err)
					}
					// Attach probes to the traversal at the matching position so
					// trie_traversal_optrace.csv can emit them later. ProbeRows is
					// position-indexed; this Get is at curPosition, so we expect
					// ProbeRows length == curPosition before append.
					if at, ok := activeMap.get(gidStr); ok && at.trav.ID == curTravID {
						for len(at.trav.ProbeRows) < curPosition {
							at.trav.ProbeRows = append(at.trav.ProbeRows, nil)
						}
						at.trav.ProbeRows = append(at.trav.ProbeRows, boundProbes)
					}
				} else {
					// Non-Trie/Snap category Get on this gid — drop pending probes for
					// it (they belong to a Header/Body/Code lookup we don't analyze).
					_, _ = probes.onGet(gidStr, key, 0, 0)
				}
				// Note: Gets in non-Trie/Snap categories (Header, Body, Code, ...)
				// are intentionally left to pass through without disturbing any
				// active Trie/Snap traversal — matching analyzeTrieTraversal.py,
				// which simply skips them via category filter.
			} else if gidStr != "" {
				// Get line without a usable key prefix (very rare) — also clear
				// pending so probes don't leak across goroutines.
				_, _ = probes.onGet(gidStr, key, 0, 0)
			}

			if pfGIDs[gidStr] {
				pfStats.GetsByCategory[cat]++
				pfStats.LevelDist[level]++
				if pfLastLevel != sentinel {
					if sstable != pfLastSSTable {
						pfStats.CrossByCategory[cat]++
					} else {
						pfStats.SameByCategory[cat]++
					}
					if level != pfLastLevel {
						pfStats.CrossLevelByCategory[cat]++
					}
				}
				pfLastLevel = level
				pfLastSSTable = sstable
			} else {
				npfStats.GetsByCategory[cat]++
				npfStats.LevelDist[level]++
				if npfLastLevel != sentinel {
					if sstable != npfLastSSTable {
						npfStats.CrossByCategory[cat]++
					} else {
						npfStats.SameByCategory[cat]++
					}
					if level != npfLastLevel {
						npfStats.CrossLevelByCategory[cat]++
					}
				}
				npfLastLevel = level
				npfLastSSTable = sstable
			}
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

	// Flush any traversals still in flight at EOF, preserving the gid insertion
	// order within each domain (Trie first, then Snap) — matches Py's
	// `for _, trav in active.values()` iteration over a Python 3.7+ dict.
	finishedTraversals = append(finishedTraversals, activeTrieTraversals.flush()...)
	finishedTraversals = append(finishedTraversals, activeSnapTraversals.flush()...)
	fmt.Printf("Trie/Snap traversals collected: %d\n", len(finishedTraversals))

	// Finalize the probe CSV: drop any probes whose Get event never appeared
	// (orphans), flush the csv.Writer buffer, and close the file via deferred
	// Close. Report counters so the operator can sanity-check the run.
	probes.flushAtEOF()
	probeCSV.Flush()
	if err := probeCSV.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "probe csv flush: %v\n", err)
	}
	fmt.Printf("Probe rows written: %d   orphans dropped: %d   → %s\n",
		probes.written, probes.dropped, probeCSVPath)

	writeSummary(outputDir, allStats)
	writePrefetcherSection(outputDir, pfGIDs, pfStats, npfStats, allStats)
	writeTransitionMatrix(outputDir, transMatrix, allStats)

	// --- Trie traversal analysis (ported from analyzeTrieTraversal.py) ---
	metricsList := computeAllMetrics(finishedTraversals)
	if err := writeTraversalDetailCSV(outputDir, metricsList); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write traversal detail CSV: %v\n", err)
	}
	if err := writeTraversalSummary(outputDir, metricsList, finishedTraversals); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write traversal summary: %v\n", err)
	}

	// --- End-to-end (TrieAccount → TrieStorage) analysis ---
	matched, unmatched, totalAcct := runEndToEnd(finishedTraversals)
	if err := writeEndToEndCSV(outputDir, matched); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write end-to-end CSV: %v\n", err)
	}
	if err := writeEndToEndSummary(outputDir, matched, unmatched, totalAcct); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write end-to-end summary: %v\n", err)
	}

	// --- read_op grouping → trie_traversal_optrace.csv ---
	trav2op, ops := assignReadOps(finishedTraversals)
	fmt.Printf("Read ops assigned: %d (covering %d Trie traversals)\n", len(ops), len(trav2op))
	if err := writeTrieTraversalBlktraceCSV(outputDir, finishedTraversals, trav2op, ops); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write trie_traversal_optrace: %v\n", err)
	}
	if err := writeReadOpSummary(outputDir, ops); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write read op summary: %v\n", err)
	}

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

func writePrefetcherSection(outputDir string, pfGIDs map[string]bool, pfStats, npfStats *PrefetcherStats, allStats map[string]*CategoryStats) {
	path := outputDir + "/ra_summary.txt"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open %s for appending: %v\n", path, err)
		return
	}
	defer f.Close()

	// Compute global Gets from allStats
	globalGets := 0
	for _, s := range allStats {
		globalGets += s.GetTotal
	}

	// Compute totals per group
	pfTotal := 0
	for _, v := range pfStats.GetsByCategory {
		pfTotal += v
	}
	npfTotal := 0
	for _, v := range npfStats.GetsByCategory {
		npfTotal += v
	}

	fmt.Fprintln(f, "\n\n=== Prefetcher Analysis ===")
	fmt.Fprintln(f, "(Based on goroutine ID matching: PREFETCHER_START gid vs OPType: Get gid)")
	fmt.Fprintln(f)
	fmt.Fprintf(f, "  Unique Prefetcher GIDs seen: %d\n", len(pfGIDs))
	if globalGets > 0 {
		fmt.Fprintf(f, "  Total Prefetcher Gets:     %12d  (%6.2f%% of all Gets)\n", pfTotal, 100.0*float64(pfTotal)/float64(globalGets))
		fmt.Fprintf(f, "  Total Non-Prefetcher Gets: %12d  (%6.2f%% of all Gets)\n", npfTotal, 100.0*float64(npfTotal)/float64(globalGets))
	}

	// Helper: sort categories by count descending
	type catCount struct {
		cat   string
		count int
	}
	sortByCount := func(m map[string]int) []catCount {
		entries := make([]catCount, 0, len(m))
		for cat, cnt := range m {
			entries = append(entries, catCount{cat, cnt})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })
		return entries
	}
	writeLevelDist := func(dist map[int]int, total int) {
		levels := make([]int, 0, len(dist))
		for lvl := range dist {
			levels = append(levels, lvl)
		}
		sort.Ints(levels)
		for _, lvl := range levels {
			cnt := dist[lvl]
			pct := 100.0 * float64(cnt) / float64(total)
			lvlName := fmt.Sprintf("L%d", lvl)
			if lvl == -1 {
				lvlName = "L-1(notfound)"
			}
			fmt.Fprintf(f, "    %-18s %12d  (%6.2f%%)\n", lvlName+":", cnt, pct)
		}
	}

	// --- Prefetcher Gets by Category ---
	fmt.Fprintln(f, "\n  --- Prefetcher Gets by Category ---")
	for i, e := range sortByCount(pfStats.GetsByCategory) {
		pct := 0.0
		if pfTotal > 0 {
			pct = 100.0 * float64(e.count) / float64(pfTotal)
		}
		fmt.Fprintf(f, "    %d. %-30s %12d  (%6.2f%% of prefetch Gets)\n", i+1, e.cat, e.count, pct)
	}

	// --- Prefetcher Cross-SSTable by Category ---
	fmt.Fprintln(f, "\n  --- Prefetcher Cross-SSTable by Category ---")
	for i, e := range sortByCount(pfStats.CrossByCategory) {
		compared := pfStats.GetsByCategory[e.cat]
		pct := 0.0
		if compared > 0 {
			pct = 100.0 * float64(e.count) / float64(compared)
		}
		fmt.Fprintf(f, "    %d. %-30s %8d cross / %8d compared  (%6.2f%%)\n", i+1, e.cat, e.count, compared, pct)
	}

	// --- Prefetcher Level Distribution ---
	fmt.Fprintln(f, "\n  --- Prefetcher Level Distribution (all categories) ---")
	writeLevelDist(pfStats.LevelDist, pfTotal)

	// --- Non-Prefetcher Gets by Category ---
	fmt.Fprintln(f, "\n  --- Non-Prefetcher Gets by Category ---")
	for i, e := range sortByCount(npfStats.GetsByCategory) {
		pct := 0.0
		if npfTotal > 0 {
			pct = 100.0 * float64(e.count) / float64(npfTotal)
		}
		fmt.Fprintf(f, "    %d. %-30s %12d  (%6.2f%% of non-prefetch Gets)\n", i+1, e.cat, e.count, pct)
	}

	// --- Non-Prefetcher Cross-SSTable by Category ---
	fmt.Fprintln(f, "\n  --- Non-Prefetcher Cross-SSTable by Category ---")
	for i, e := range sortByCount(npfStats.CrossByCategory) {
		compared := npfStats.GetsByCategory[e.cat]
		pct := 0.0
		if compared > 0 {
			pct = 100.0 * float64(e.count) / float64(compared)
		}
		fmt.Fprintf(f, "    %d. %-30s %8d cross / %8d compared  (%6.2f%%)\n", i+1, e.cat, e.count, compared, pct)
	}

	// --- Non-Prefetcher Level Distribution ---
	fmt.Fprintln(f, "\n  --- Non-Prefetcher Level Distribution (all categories) ---")
	writeLevelDist(npfStats.LevelDist, npfTotal)

	// --- Comparison table ---
	pfCross, pfSame, pfCrossLevel := 0, 0, 0
	for _, v := range pfStats.CrossByCategory {
		pfCross += v
	}
	for _, v := range pfStats.SameByCategory {
		pfSame += v
	}
	for _, v := range pfStats.CrossLevelByCategory {
		pfCrossLevel += v
	}
	npfCross, npfSame, npfCrossLevel := 0, 0, 0
	for _, v := range npfStats.CrossByCategory {
		npfCross += v
	}
	for _, v := range npfStats.SameByCategory {
		npfSame += v
	}
	for _, v := range npfStats.CrossLevelByCategory {
		npfCrossLevel += v
	}

	pfCompared := pfCross + pfSame
	npfCompared := npfCross + npfSame
	var pfCrossPct, pfCrossLevelPct, npfCrossPct, npfCrossLevelPct float64
	if pfCompared > 0 {
		pfCrossPct = 100.0 * float64(pfCross) / float64(pfCompared)
		pfCrossLevelPct = 100.0 * float64(pfCrossLevel) / float64(pfCompared)
	}
	if npfCompared > 0 {
		npfCrossPct = 100.0 * float64(npfCross) / float64(npfCompared)
		npfCrossLevelPct = 100.0 * float64(npfCrossLevel) / float64(npfCompared)
	}

	fmt.Fprintln(f, "\n  --- Prefetcher vs Non-Prefetcher Comparison ---")
	fmt.Fprintf(f, "    %-30s %11s  %11s\n", "", "Prefetcher", "Non-Prefetch")
	fmt.Fprintf(f, "    %-30s %11d  %11d\n", "Total Gets:", pfTotal, npfTotal)
	fmt.Fprintf(f, "    %-30s %10.2f%%  %10.2f%%\n", "Cross-SSTable:", pfCrossPct, npfCrossPct)
	fmt.Fprintf(f, "    %-30s %10.2f%%  %10.2f%%\n", "Cross-Level:", pfCrossLevelPct, npfCrossLevelPct)

	fmt.Printf("Prefetcher analysis appended to %s\n", path)
}

// ---------------------------------------------------------------------------
// Traversal analysis output
// ---------------------------------------------------------------------------

func computeAllMetrics(travs []*Traversal) []TraversalMetrics {
	out := make([]TraversalMetrics, len(travs))
	for i, t := range travs {
		out[i] = t.computeMetrics()
	}
	return out
}

var traversalDetailHeader = []string{
	// traversal_id is the join key for pebble_probes.csv (one probe per row,
	// each carrying a traversal_id + position).
	"traversal_id",
	"block_id", "goroutine_type", "gid", "category", "account_hash",
	"root_key", "leaf_key", "depth",
	"num_unique_sstables", "num_sstable_transitions",
	"num_unique_data_blocks", "num_data_block_transitions",
	"cache_hits", "cache_misses", "first_key_cache_hit",
	"subsequent_cache_hit_rate",
	"total_latency_ns", "first_key_latency_ns", "avg_subsequent_latency_ns",
	"levels_accessed", "num_level_transitions",
	// Ordered per-Get sequences for inspecting MPT-walk vs LSM-layout.
	"root_level", "leaf_level",
	"level_sequence", "sstable_sequence",
}

// roundTo rounds v to the given number of decimal places using banker's
// rounding (round half to even), matching Python 3's built-in round().
func roundTo(v float64, decimals int) float64 {
	shift := math.Pow(10, float64(decimals))
	return math.RoundToEven(v*shift) / shift
}

// formatPyFloat mirrors Python's str(float) representation: shortest decimal
// form, but with a forced ".0" for whole-number values (e.g. -1 → "-1.0").
func formatPyFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// formatPyBool mirrors Python's str(bool) — capital first letter.
func formatPyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

func writeTraversalDetailCSV(outputDir string, metricsList []TraversalMetrics) error {
	path := outputDir + "/traversal_detail.csv"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.UseCRLF = true // match Python csv module default (RFC 4180)
	defer w.Flush()
	if err := w.Write(traversalDetailHeader); err != nil {
		return err
	}
	for _, m := range metricsList {
		// Match Py's analyzeTrieTraversal.py: only Trie* traversals are
		// written to the detail CSV. Snap* traversals are tracked separately
		// (they only contribute to summary cache-pattern stats).
		if m.Category != "TrieAccount" && m.Category != "TrieStorage" {
			continue
		}
		row := []string{
			strconv.FormatInt(m.ID, 10),
			m.BlockID,
			m.GoroutineType,
			m.GID,
			m.Category,
			m.AccountHash,
			m.RootKey,
			m.LeafKey,
			strconv.Itoa(m.Depth),
			strconv.Itoa(m.NumUniqueSstables),
			strconv.Itoa(m.NumSstableTransitions),
			strconv.Itoa(m.NumUniqueDataBlocks),
			strconv.Itoa(m.NumDataBlockTransitions),
			strconv.Itoa(m.CacheHits),
			strconv.Itoa(m.CacheMisses),
			formatPyBool(m.FirstKeyCacheHit),
			formatPyFloat(roundTo(m.SubsequentCacheHitRate, 4)),
			strconv.FormatInt(m.TotalLatencyNs, 10),
			strconv.FormatInt(m.FirstKeyLatencyNs, 10),
			formatPyFloat(roundTo(m.AvgSubsequentLatencyNs, 1)),
			m.LevelsAccessed,
			strconv.Itoa(m.NumLevelTransitions),
			strconv.Itoa(m.RootLevel),
			strconv.Itoa(m.LeafLevel),
			m.LevelSequence,
			m.SstableSequence,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	fmt.Printf("Traversal detail written to %s\n", path)
	return nil
}

// writeTraversalSummary mirrors analyzeTrieTraversal.py's write_summary.
func writeTraversalSummary(outputDir string, metricsList []TraversalMetrics, travs []*Traversal) error {
	path := outputDir + "/traversal_summary.txt"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(metricsList) == 0 {
		fmt.Fprintln(f, "No traversals found.")
		return nil
	}

	fmt.Fprintln(f, strings.Repeat("=", 70))
	fmt.Fprintln(f, "  Trie Traversal Analysis Summary")
	fmt.Fprintln(f, strings.Repeat("=", 70))

	// Build groups in the same order Py uses.
	groupOrder := []string{
		"ALL",
		"goroutine:main_thread",
		"goroutine:prefetcher",
		"category:TrieAccount",
		"category:TrieStorage",
		"category:SnapAccount",
		"category:SnapStorage",
	}
	groups := make(map[string][]TraversalMetrics)
	for _, m := range metricsList {
		groups["ALL"] = append(groups["ALL"], m)
		groups["goroutine:"+m.GoroutineType] = append(groups["goroutine:"+m.GoroutineType], m)
		groups["category:"+m.Category] = append(groups["category:"+m.Category], m)
	}

	for _, label := range groupOrder {
		subset := groups[label]
		if len(subset) == 0 {
			continue
		}
		fmt.Fprintln(f)
		fmt.Fprintf(f, "--- %s (%d traversals) ---\n", label, len(subset))
		fmt.Fprintln(f)

		depths := make([]int, len(subset))
		for i, m := range subset {
			depths[i] = m.Depth
		}
		fmt.Fprintf(f, "  Traversal depth:  avg=%.2f  median=%.0f  min=%d  max=%d\n",
			meanInt(depths), medianInt(depths), minInt(depths), maxInt(depths))

		// Depth distribution: 1..5 individually then 6+
		dCounts := make(map[string]int)
		for _, d := range depths {
			if d <= 5 {
				dCounts[strconv.Itoa(d)]++
			} else {
				dCounts["6+"]++
			}
		}
		dKeys := make([]string, 0, len(dCounts))
		for k := range dCounts {
			dKeys = append(dKeys, k)
		}
		sort.Strings(dKeys)
		var depthParts []string
		for _, k := range dKeys {
			depthParts = append(depthParts, fmt.Sprintf("%s=%d", k, dCounts[k]))
		}
		fmt.Fprintf(f, "  Depth dist:  %s\n", strings.Join(depthParts, "  "))

		// SSTable transitions
		sstTrans := make([]int, len(subset))
		for i, m := range subset {
			sstTrans[i] = m.NumSstableTransitions
		}
		nZero, nOne, nTwoPlus := 0, 0, 0
		for _, s := range sstTrans {
			switch {
			case s == 0:
				nZero++
			case s == 1:
				nOne++
			default:
				nTwoPlus++
			}
		}
		total := len(subset)
		fmt.Fprintf(f, "  SSTable transitions:  0=%d (%.1f%%)  1=%d (%.1f%%)  2+=%d (%.1f%%)\n",
			nZero, 100*float64(nZero)/float64(total),
			nOne, 100*float64(nOne)/float64(total),
			nTwoPlus, 100*float64(nTwoPlus)/float64(total))
		fmt.Fprintf(f, "  Avg SSTable transitions per traversal: %.2f\n", meanInt(sstTrans))

		// Data block transitions
		blkTrans := make([]int, len(subset))
		for i, m := range subset {
			blkTrans[i] = m.NumDataBlockTransitions
		}
		nBlkZero := 0
		for _, b := range blkTrans {
			if b == 0 {
				nBlkZero++
			}
		}
		fmt.Fprintf(f, "  Data block transitions:  all-same-block=%d (%.1f%%)  avg_transitions=%.2f\n",
			nBlkZero, 100*float64(nBlkZero)/float64(total), meanInt(blkTrans))

		// Cache patterns (depth > 1 only)
		var multi []TraversalMetrics
		for _, m := range subset {
			if m.Depth > 1 {
				multi = append(multi, m)
			}
		}
		if len(multi) > 0 {
			firstMiss := 0
			for _, m := range multi {
				if !m.FirstKeyCacheHit {
					firstMiss++
				}
			}
			firstHit := len(multi) - firstMiss
			var subRates []float64
			for _, m := range multi {
				if m.SubsequentCacheHitRate >= 0 {
					subRates = append(subRates, m.SubsequentCacheHitRate)
				}
			}
			avgSubRate := meanFloat(subRates)
			fmt.Fprintf(f, "  Cache (depth>1 only, N=%d):\n", len(multi))
			fmt.Fprintf(f, "    First key:  miss=%d (%.1f%%)  hit=%d (%.1f%%)\n",
				firstMiss, 100*float64(firstMiss)/float64(len(multi)),
				firstHit, 100*float64(firstHit)/float64(len(multi)))
			fmt.Fprintf(f, "    Subsequent keys avg hit rate: %.1f%%\n", 100*avgSubRate)
		}

		// Overall cache hit rate
		var totalHits, totalMisses int
		for _, m := range subset {
			totalHits += m.CacheHits
			totalMisses += m.CacheMisses
		}
		totalOps := totalHits + totalMisses
		if totalOps > 0 {
			fmt.Fprintf(f, "  Overall cache:  hits=%d (%.1f%%)  misses=%d (%.1f%%)\n",
				totalHits, 100*float64(totalHits)/float64(totalOps),
				totalMisses, 100*float64(totalMisses)/float64(totalOps))
		}

		// Latency
		firstLat := make([]int64, len(subset))
		for i, m := range subset {
			firstLat[i] = m.FirstKeyLatencyNs
		}
		fmt.Fprintf(f, "  Latency (first key):  avg=%.2f ms  median=%.2f ms\n",
			meanInt64(firstLat)/1e6, medianInt64(firstLat)/1e6)

		if len(multi) > 0 {
			subLat := make([]float64, len(multi))
			for i, m := range multi {
				subLat[i] = m.AvgSubsequentLatencyNs
			}
			// median of float — quick adapter
			subLatInt := make([]int64, len(subLat))
			for i, v := range subLat {
				subLatInt[i] = int64(v)
			}
			fmt.Fprintf(f, "  Latency (subsequent):  avg=%.2f ms  median=%.2f ms\n",
				meanFloat(subLat)/1e6, medianInt64(subLatInt)/1e6)
		}

		// Level transitions
		lvlTrans := make([]int, len(subset))
		for i, m := range subset {
			lvlTrans[i] = m.NumLevelTransitions
		}
		nLvlZero := 0
		for _, l := range lvlTrans {
			if l == 0 {
				nLvlZero++
			}
		}
		fmt.Fprintf(f, "  Level transitions:  all-same-level=%d (%.1f%%)  avg=%.2f\n",
			nLvlZero, 100*float64(nLvlZero)/float64(total), meanInt(lvlTrans))
	}

	fmt.Printf("Traversal summary written to %s\n", path)
	return nil
}

// ---------------------------------------------------------------------------
// End-to-end (TrieAccount + TrieStorage) analysis
// ---------------------------------------------------------------------------

// E2EMatch is one row of end_to_end_detail.csv.
type E2EMatch struct {
	BlockID                 string
	AccountHash             string
	AccountTrieDepth        int
	StorageSlotCount        int
	CombinedDepth           int
	CombinedSstTransitions  int
	AccountToStorageCrossSst int
	CombinedCacheMissRate   float64
	CombinedLatencyNs       int64
}

// runEndToEnd matches each TrieAccount traversal to TrieStorage traversals on
// the same block whose account_hash starts with the nibble-derived prefix.
// Returns matched rows, count of unmatched accounts (EOAs / no storage access),
// and total account traversal count.
func runEndToEnd(travs []*Traversal) (matched []E2EMatch, unmatchedAcct int, totalAcct int) {
	var acctTravs, storTravs []*Traversal
	for _, t := range travs {
		switch t.Category {
		case "TrieAccount":
			acctTravs = append(acctTravs, t)
		case "TrieStorage":
			storTravs = append(storTravs, t)
		}
	}
	totalAcct = len(acctTravs)
	fmt.Printf("  End-to-end: %d account traversals, %d storage traversals\n",
		len(acctTravs), len(storTravs))

	// Index storage traversals by (block_id, account_hash).
	type storKey struct{ block, hash string }
	storByAccount := make(map[storKey][]*Traversal)
	uniqueHashes := make(map[string]map[string]struct{}) // block_id -> set of hashes
	for _, st := range storTravs {
		k := storKey{st.BlockID, st.AccountHash}
		storByAccount[k] = append(storByAccount[k], st)
		if uniqueHashes[st.BlockID] == nil {
			uniqueHashes[st.BlockID] = make(map[string]struct{})
		}
		uniqueHashes[st.BlockID][st.AccountHash] = struct{}{}
	}

	for _, at := range acctTravs {
		leafKey := at.Keys[len(at.Keys)-1]
		hashPrefix := nibblePathToHashPrefix(leafKey)

		blockHashes := uniqueHashes[at.BlockID]
		var matchedHashes []string
		for h := range blockHashes {
			if strings.HasPrefix(h, hashPrefix) {
				matchedHashes = append(matchedHashes, h)
			}
		}

		if len(matchedHashes) == 0 {
			unmatchedAcct++
			continue
		}

		atM := at.computeMetrics()

		for _, fullHash := range matchedHashes {
			storageList := storByAccount[storKey{at.BlockID, fullHash}]

			var storDepth, storSstTrans, storCacheMiss, storCacheHit int
			var storLat int64
			for _, s := range storageList {
				storDepth += len(s.Keys)
				for i := 1; i < len(s.Sstables); i++ {
					if s.Sstables[i] != s.Sstables[i-1] {
						storSstTrans++
					}
				}
				for _, h := range s.CacheHits {
					if h {
						storCacheHit++
					} else {
						storCacheMiss++
					}
				}
				for _, l := range s.Latencies {
					storLat += l
				}
			}

			// Cross-SSTable between account leaf and first storage root
			acctLeafSst := at.Sstables[len(at.Sstables)-1]
			firstStorSst := storageList[0].Sstables[0]
			crossSst := 0
			if acctLeafSst != firstStorSst {
				crossSst = 1
			}

			combinedDepth := atM.Depth + storDepth
			combinedSst := atM.NumSstableTransitions + crossSst + storSstTrans
			combinedMiss := atM.CacheMisses + storCacheMiss
			combinedHit := atM.CacheHits + storCacheHit
			combinedLat := atM.TotalLatencyNs + storLat
			ops := combinedMiss + combinedHit
			missRate := 0.0
			if ops > 0 {
				missRate = float64(combinedMiss) / float64(ops)
			}

			matched = append(matched, E2EMatch{
				BlockID:                  at.BlockID,
				AccountHash:              fullHash,
				AccountTrieDepth:         atM.Depth,
				StorageSlotCount:         len(storageList),
				CombinedDepth:            combinedDepth,
				CombinedSstTransitions:   combinedSst,
				AccountToStorageCrossSst: crossSst,
				CombinedCacheMissRate:    missRate,
				CombinedLatencyNs:        combinedLat,
			})
		}
	}

	fmt.Printf("  Matched: %d account→storage pairs, %d accounts without storage (EOA or no storage access)\n",
		len(matched), unmatchedAcct)
	return matched, unmatchedAcct, totalAcct
}

var e2eDetailHeader = []string{
	"block_id", "account_hash",
	"account_trie_depth", "storage_slot_count",
	"combined_depth", "combined_sst_transitions",
	"account_to_storage_cross_sst",
	"combined_cache_miss_rate", "combined_latency_ns",
}

func writeEndToEndCSV(outputDir string, matched []E2EMatch) error {
	path := outputDir + "/end_to_end_detail.csv"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.UseCRLF = true // match Python csv default
	defer w.Flush()
	if err := w.Write(e2eDetailHeader); err != nil {
		return err
	}
	for _, m := range matched {
		row := []string{
			m.BlockID,
			m.AccountHash,
			strconv.Itoa(m.AccountTrieDepth),
			strconv.Itoa(m.StorageSlotCount),
			strconv.Itoa(m.CombinedDepth),
			strconv.Itoa(m.CombinedSstTransitions),
			strconv.Itoa(m.AccountToStorageCrossSst),
			formatPyFloat(roundTo(m.CombinedCacheMissRate, 4)),
			strconv.FormatInt(m.CombinedLatencyNs, 10),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	fmt.Printf("End-to-end detail written to %s\n", path)
	return nil
}

func writeEndToEndSummary(outputDir string, matched []E2EMatch, unmatched, totalAcct int) error {
	path := outputDir + "/end_to_end_summary.txt"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintln(f, strings.Repeat("=", 70))
	fmt.Fprintln(f, "  End-to-End Storage Read Analysis (TrieAccount + TrieStorage)")
	fmt.Fprintln(f, strings.Repeat("=", 70))
	fmt.Fprintln(f)

	if len(matched) == 0 {
		fmt.Fprintln(f, "No matched account->storage pairs found.")
		return nil
	}

	// 1. Contract vs EOA
	fmt.Fprintln(f, "--- 1. Contract vs EOA ---")
	fmt.Fprintf(f, "  Total account lookups: %d\n", totalAcct)
	if totalAcct > 0 {
		fmt.Fprintf(f, "  Contracts (have storage): %d (%.1f%%)\n",
			len(matched), 100*float64(len(matched))/float64(totalAcct))
		fmt.Fprintf(f, "  EOA / no storage access:  %d (%.1f%%)\n",
			unmatched, 100*float64(unmatched)/float64(totalAcct))
	}
	fmt.Fprintln(f)

	// 2. Read amplification
	acctDepths := make([]int, len(matched))
	combDepths := make([]int, len(matched))
	slotCounts := make([]int, len(matched))
	for i, m := range matched {
		acctDepths[i] = m.AccountTrieDepth
		combDepths[i] = m.CombinedDepth
		slotCounts[i] = m.StorageSlotCount
	}
	fmt.Fprintln(f, "--- 2. Read Amplification (PebbleDB Get count) ---")
	fmt.Fprintf(f, "  Account trie:  avg=%.2f\n", meanInt(acctDepths))
	fmt.Fprintf(f, "  Combined:      avg=%.2f  median=%.0f  min=%d  max=%d\n",
		meanInt(combDepths), medianInt(combDepths), minInt(combDepths), maxInt(combDepths))
	fmt.Fprintf(f, "  Avg storage slots per contract: %.1f\n", meanInt(slotCounts))
	fmt.Fprintln(f)

	// 3. SSTable transitions
	crossSst := 0
	combSst := make([]int, len(matched))
	for i, m := range matched {
		crossSst += m.AccountToStorageCrossSst
		combSst[i] = m.CombinedSstTransitions
	}
	fmt.Fprintln(f, "--- 3. SSTable Transitions ---")
	fmt.Fprintf(f, "  Account->Storage cross-SST: %d/%d (%.1f%%)\n",
		crossSst, len(matched), 100*float64(crossSst)/float64(len(matched)))
	fmt.Fprintf(f, "  Combined total:  avg=%.2f  median=%.0f  min=%d  max=%d\n",
		meanInt(combSst), medianInt(combSst), minInt(combSst), maxInt(combSst))
	fmt.Fprintln(f)

	// 4. Cache & Latency
	missRates := make([]float64, len(matched))
	combLat := make([]int64, len(matched))
	for i, m := range matched {
		missRates[i] = m.CombinedCacheMissRate
		combLat[i] = m.CombinedLatencyNs
	}
	fmt.Fprintln(f, "--- 4. Cache & Latency ---")
	fmt.Fprintf(f, "  Avg cache miss rate: %.1f%%\n", 100*meanFloat(missRates))
	fmt.Fprintf(f, "  Latency:  avg=%.2f ms  median=%.2f ms\n",
		meanInt64(combLat)/1e6, medianInt64(combLat)/1e6)

	fmt.Printf("End-to-end summary written to %s\n", path)
	return nil
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

// ---------------------------------------------------------------------------
// read_op grouping + trie_traversal_optrace.csv
// ---------------------------------------------------------------------------
//
// A read_op is one logical state read: either an account_read (TrieAccount
// walk only), a contract_read (TrieAccount walk + ≥1 TrieStorage walks under
// that account), or a storage_only (TrieStorage walk(s) without a preceding
// TrieAccount because the account was already in the snapshot/trie cache).
//
// Grouping rules (per-goroutine, sorted by trav_id which is temporal order):
//   - TrieAccount always closes the current op and opens a new one
//   - TrieStorage extends the current op if (same block) and (account_hash
//     matches: either equals curOp.AccountHash, or curOp.AccountHash is a
//     prefix derived from the TrieAccount leaf nibble path)
//   - Otherwise TrieStorage opens a new op (storage_only)
//   - Block boundary always closes the op
type readOp struct {
	ID          int64
	OpType      string // "account_read" | "contract_read" | "storage_only"
	BlockID     string
	GID         string
	AccountHash string // full 64-hex when storage seen; prefix from TrieAccount otherwise
	TravIDs     []int64
}

// activeOpState tracks an in-progress read_op while walking traversals in
// temporal order. Fields beyond readOp are used only during the build loop:
// hasAccount/hasStorage drive op_type, and we keep a pointer-style identity to
// allow O(N) linear search of the per-block active set.
type activeOpState struct {
	op          readOp
	hasAccount  bool
	hasStorage  bool
}

// assignReadOps walks all Trie traversals globally in trav_id (= temporal)
// order and groups them into read_ops by (block, account_hash). Goroutine
// identity is NOT a grouping constraint because Geth's prefetcher fans out one
// goroutine per (account, slot) — so an account_read on gid_A and the related
// storage_reads on gid_B/gid_C are still the same logical state read.
//
// Grouping rules:
//   - Same (block, account) accumulates: TrieAccount (≤1) + TrieStorage (≥0)
//   - If a SECOND TrieAccount for an already-finished (block, account) arrives,
//     close the prior op and open a new one (user rule: same account read twice
//     in the same block = two separate read_ops)
//   - Block boundary closes every active op in the previous block
//
// Hash matching is tolerant because TrieAccount leaves only carry a nibble
// prefix of the account hash, while TrieStorage keys carry the full 64-char
// hash. Two account hash representations match if either is a prefix of the
// other.
func assignReadOps(travs []*Traversal) (map[int64]int64, []readOp) {
	// Filter to Trie only, sort by trav_id (= temporal order).
	var trieTravs []*Traversal
	for _, t := range travs {
		if t.Category == "TrieAccount" || t.Category == "TrieStorage" {
			trieTravs = append(trieTravs, t)
		}
	}
	sort.Slice(trieTravs, func(i, j int) bool {
		return trieTravs[i].ID < trieTravs[j].ID
	})

	trav2op := make(map[int64]int64, len(trieTravs))
	var ops []readOp
	var nextOpID int64 = 0

	// active set for the current block.
	var active []*activeOpState
	lastBlock := ""

	flushBlock := func() {
		for _, st := range active {
			ops = append(ops, st.op)
		}
		active = active[:0]
	}

	setOpType := func(st *activeOpState) {
		switch {
		case st.hasAccount && st.hasStorage:
			st.op.OpType = "contract_read"
		case st.hasAccount:
			st.op.OpType = "account_read"
		default:
			st.op.OpType = "storage_only"
		}
	}

	for _, t := range trieTravs {
		if t.BlockID != lastBlock {
			flushBlock()
			lastBlock = t.BlockID
		}

		isAccount := t.Category == "TrieAccount"
		var travHash string
		if isAccount {
			travHash = nibblePathToHashPrefix(t.Keys[len(t.Keys)-1])
		} else {
			travHash = t.AccountHash
		}

		// Find a matching active op in this block. Match if either hash is a
		// prefix of the other (TrieAccount's nibble-derived prefix vs
		// TrieStorage's full 64-char hash).
		matchIdx := -1
		for i, st := range active {
			oh := st.op.AccountHash
			if oh == travHash || strings.HasPrefix(oh, travHash) || strings.HasPrefix(travHash, oh) {
				matchIdx = i
				break
			}
		}

		if matchIdx >= 0 && isAccount && active[matchIdx].hasAccount {
			// Second TrieAccount for the same (block, account) → split.
			// Close the prior op, then fall through to open a fresh one.
			ops = append(ops, active[matchIdx].op)
			active = append(active[:matchIdx], active[matchIdx+1:]...)
			matchIdx = -1
		}

		if matchIdx >= 0 {
			st := active[matchIdx]
			if isAccount {
				st.hasAccount = true
			} else {
				st.hasStorage = true
				// Storage carries the canonical full hash — promote.
				if len(travHash) > len(st.op.AccountHash) {
					st.op.AccountHash = travHash
				}
			}
			st.op.TravIDs = append(st.op.TravIDs, t.ID)
			setOpType(st)
			trav2op[t.ID] = st.op.ID
		} else {
			nextOpID++
			st := &activeOpState{
				op: readOp{
					ID:          nextOpID,
					BlockID:     t.BlockID,
					GID:         t.GID, // first gid that touched this op
					AccountHash: travHash,
					TravIDs:     []int64{t.ID},
				},
			}
			if isAccount {
				st.hasAccount = true
			} else {
				st.hasStorage = true
			}
			setOpType(st)
			active = append(active, st)
			trav2op[t.ID] = nextOpID
		}
	}
	flushBlock() // close the final block

	return trav2op, ops
}

var trieTravBlktraceHeader = []string{
	"read_op_id", "op_type", "event_type",
	"block_id", "gid", "goroutine_type",
	"trav_id", "category", "account_hash",
	"position", "probe_idx",
	"key", "level", "sstable",
	"filter_present", "filter_cache_hit", "filter_positive", "index_cache_hit", "found", "data_cache_hit",
	"latency_ns",
}

// writeTrieTraversalBlktraceCSV emits one row per event (Get or Probe) grouped
// by read_op_id. Rows are sorted by (read_op_id, trav_id, position, probe_idx)
// so that a `sort -t, -k1,1 -k7,7 -k10,10 -k11,11` is enough to keep a single
// read_op's events together for visual inspection.
//
// For event_type=get: probe_idx is empty, filter_present/filter_positive/found
// are empty (the Get's "found" is implicit), data_cache_hit comes from the
// traversal's CacheHits[position].
// For event_type=probe: all probe-specific columns are filled.
func writeTrieTraversalBlktraceCSV(outputDir string, travs []*Traversal, trav2op map[int64]int64, ops []readOp) error {
	path := outputDir + "/trie_traversal_optrace.csv"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.UseCRLF = true
	defer w.Flush()
	if err := w.Write(trieTravBlktraceHeader); err != nil {
		return err
	}

	// Index for op_type lookup.
	opTypeByID := make(map[int64]string, len(ops))
	for _, op := range ops {
		opTypeByID[op.ID] = op.OpType
	}

	// Index traversals by trav_id for lookup. Then iterate ops in ID order,
	// emitting each op's traversals in trav_id order.
	travByID := make(map[int64]*Traversal, len(travs))
	for _, t := range travs {
		travByID[t.ID] = t
	}

	var rowsWritten int64
	for _, op := range ops {
		// op.TravIDs is already in insertion order = trav_id order (we sort
		// per-gid by trav_id in assignReadOps).
		for _, tid := range op.TravIDs {
			t, ok := travByID[tid]
			if !ok {
				continue
			}
			n := len(t.Keys)
			for pos := 0; pos < n; pos++ {
				// Get row.
				dataCacheHit := "False"
				if t.CacheHits[pos] {
					dataCacheHit = "True"
				}
				getRow := []string{
					strconv.FormatInt(op.ID, 10),
					op.OpType,
					"get",
					t.BlockID,
					t.GID,
					t.GoroutineType,
					strconv.FormatInt(t.ID, 10),
					t.Category,
					op.AccountHash,
					strconv.Itoa(pos),
					"", // probe_idx empty for get
					t.Keys[pos],
					strconv.Itoa(t.Levels[pos]),
					strconv.Itoa(t.Sstables[pos]),
					"", // filter_present (probe-only)
					"", // filter_cache_hit (probe-only)
					"", // filter_positive (probe-only)
					"", // index_cache_hit (probe-only)
					"", // found (probe-only; Get's "found" is implicit)
					dataCacheHit,
					strconv.FormatInt(t.Latencies[pos], 10),
				}
				if err := w.Write(getRow); err != nil {
					return err
				}
				rowsWritten++

				// Probe rows for this position (if any).
				if pos < len(t.ProbeRows) {
					for pi, p := range t.ProbeRows[pos] {
						probeRow := []string{
							strconv.FormatInt(op.ID, 10),
							op.OpType,
							"probe",
							t.BlockID,
							t.GID,
							t.GoroutineType,
							strconv.FormatInt(t.ID, 10),
							t.Category,
							op.AccountHash,
							strconv.Itoa(pos),
							strconv.Itoa(pi),
							p.Key,
							strconv.Itoa(p.Level),
							strconv.Itoa(p.SSTable),
							strconv.FormatBool(p.FilterPresent),
							p.FilterCacheHit,
							p.FilterPositive,
							p.IndexCacheHit,
							strconv.FormatBool(p.Found),
							p.DataCacheHit,
							strconv.FormatInt(p.LatencyNs, 10),
						}
						if err := w.Write(probeRow); err != nil {
							return err
						}
						rowsWritten++
					}
				}
			}
		}
	}
	fmt.Printf("trie_traversal_optrace rows written: %d   ops: %d   → %s\n",
		rowsWritten, len(ops), path)
	return nil
}

// writeReadOpSummary emits a short text summary of read_op statistics
// (account_read / contract_read / storage_only counts, avg trav per op, etc.).
func writeReadOpSummary(outputDir string, ops []readOp) error {
	path := outputDir + "/read_op_summary.txt"
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	counts := make(map[string]int)
	travCount := make(map[string]int)
	for _, op := range ops {
		counts[op.OpType]++
		travCount[op.OpType] += len(op.TravIDs)
	}

	fmt.Fprintln(f, strings.Repeat("=", 70))
	fmt.Fprintln(f, "  Read Op Summary (trie_traversal_optrace.csv grouping)")
	fmt.Fprintln(f, strings.Repeat("=", 70))
	fmt.Fprintf(f, "\n  Total read_ops: %d\n\n", len(ops))
	for _, ot := range []string{"account_read", "contract_read", "storage_only"} {
		c := counts[ot]
		if c == 0 {
			continue
		}
		avg := float64(travCount[ot]) / float64(c)
		fmt.Fprintf(f, "  %-15s  count=%-8d  avg_trav_per_op=%.2f\n", ot, c, avg)
	}
	fmt.Printf("Read op summary written to %s\n", path)
	return nil
}
