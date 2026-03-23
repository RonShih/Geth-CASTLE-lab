# Read Amplification Analysis

## 背景與動機

在 Ethereum Geth 中，底層 KV 資料庫使用 Pebble（LSM-Tree 架構）。
LSM-Tree 的讀取成本取決於資料落在哪一層（Level）以及哪個 SSTable 檔案。

本分析的目標：
- 確認哪些 KV pair 類型（key prefix）在 Geth 執行 block 時被讀取
- 量化連續 Get 操作之間的 **跨 SSTable / 跨 Level 情形**，作為 Read Amplification 的代理指標

---

## 分析範圍

| 項目 | 內容 |
|------|------|
| Trace 檔案 | `/home/ron/ethereum/execution/blktrace_11250000_11350000` |
| Block 範圍 | 11,250,000 – 11,350,000 (10 萬個 block) |
| 檔案大小 | 約 95 GB |
| 產生工具 | `common/globalTraceLog.go` |

---

## RA 衡量方法

### 核心定義

> 若**全域連續**兩個 Get 操作（Get_i → Get_i+1）的 `sstable` ID 不同，
> 則視為一次「跨 SSTable 轉換」，代表潛在的 Read Amplification 事件。

**全域連續**：不以 block 為邊界切割，整個 trace 的 Get 序列視為一條連續時間軸。

### 衡量指標

| 指標 | 說明 |
|------|------|
| **Cross-SSTable (incoming)** | 該 Get 與前一個 Get 的 sstable ID 不同的次數 |
| **Same-SSTable (incoming)** | 該 Get 與前一個 Get 的 sstable ID 相同的次數 |
| **Cross-Level (incoming)** | 該 Get 與前一個 Get 的 level 不同的次數 |

> 跨 SSTable 與跨 Level 衡量的現象相近，兩者趨勢高度相關。

### 歸因方式

轉換次數歸因於**目的地** Get（Get_i+1）所屬的 key prefix category。
即：「當 Geth 要讀取 category X 時，前一個 Get 有多少比例在不同的 SSTable？」

### Pebble level 欄位說明

| level 值 | 意義 |
|----------|------|
| 0 – 6 | 資料在 LSM-Tree 的對應 level |
| -1 | Key 未找到，或資料在 memtable |
| sstable = 0 | 資料在 memtable（非磁碟）|

---

## 限制與待討論事項

### 現有方法的限制

1. **非傳統 RA 定義**：傳統 RA 指一次邏輯 Get 在 LSM 內部檢查的 level/file 數量。
   本方法看的是**應用層連續 Get 之間的 SSTable 跳躍**，屬於「存取局部性（access locality）」指標。

2. **Pebble 內部 RA 不可見**：Trace log 只記錄 key 最終在哪個 SSTable 被找到，
   Pebble 在找到之前可能已掃描多個 level，這部分未被捕捉。

3. **OS Cache 效應未考慮**：若兩個不同 SSTable 的 Get 都在 page cache 中，
   實際上不會有磁碟 I/O，但本方法仍計為跨 SSTable。

### 待討論（尚未定案）

- 是否需要加入 **per-block RA score**（每個 block 中涉及的 unique SSTable 數量）作為補充？
- 是否區分 **memtable → SSTable** 與 **SSTable → SSTable** 兩種跨越類型？
- 是否需要以 transaction 為單位切割，而非全域連續？

---

## 工具說明

### 程式：`analysisReadAmplification.go`

```
Usage: ./bin/analysisReadAmplification <trace_file> [output_dir]

Arguments:
  trace_file  : blktrace log 檔案路徑
  output_dir  : 輸出目錄（預設 ./raOutput）
```

### 輸出檔案

| 檔案 | 內容 |
|------|------|
| `ra_summary.txt` | KV op 總數、level 分布、跨 SSTable/Level 統計（按 category 分類） |
| `ra_transition_matrix.csv` | Category × Category 跨 SSTable 轉換矩陣 |

### 執行方式

```bash
cd analysis

# 編譯
bash build.sh build

# 執行完整分析
bash readAmplificationAnalysis.sh

# 或自訂路徑
./bin/analysisReadAmplification \
  /home/ron/ethereum/execution/blktrace_11250000_11350000 \
  ./raOutput_11250000_11350000
```

---

## 初步結果（Smoke Test，前 50 萬行）

### KV Op 數量（前幾名）

| Category | Get | BatchPut | BatchDelete | NewIterator |
|----------|-----|----------|-------------|-------------|
| TrieNodeStoragePrefix | 157,354 | – | – | – |
| TrieNodeAccountPrefix | 142,894 | – | – | – |
| TxLookupPrefix | – | 122,267 | – | – |
| SnapshotStoragePrefix | 29,384 | – | – | – |
| SnapshotAccountPrefix | 23,759 | – | – | – |

### Get Level 分布（主要 category）

| Category | L3 | L4 | L5 | L6 | L-1(notfound) |
|----------|----|----|----|-----|----------------|
| TrieNodeStoragePrefix | 10.46% | 34.21% | 32.09% | 23.24% | 0% |
| TrieNodeAccountPrefix | 1.47% | 24.81% | 45.14% | 28.58% | 0% |
| SnapshotStoragePrefix | 0% | 22.23% | 45.46% | 32.30% | 0% |
| SnapshotAccountPrefix | 0% | 23.87% | 50.38% | 25.75% | 0% |
| HeaderPrefix | 0% | 0% | 0% | 45.88% | 54.12% |

> 觀察：Trie node 幾乎全落在 L4–L6（深層），代表每次讀取都需要讀磁碟。

### Read Amplification 統計

| Category | Total Gets | Cross-SSTable | Same-SSTable | Cross-Level |
|----------|-----------|--------------|--------------|-------------|
| SnapshotAccountPrefix | 23,759 | **98.81%** | 1.19% | 57.64% |
| SnapshotStoragePrefix | 29,384 | **68.42%** | 31.58% | 43.15% |
| TrieNodeStoragePrefix | 157,354 | **59.00%** | 41.00% | 46.72% |
| TrieNodeAccountPrefix | 142,894 | **51.23%** | 48.77% | 37.04% |
| HeaderPrefix | 3,487 | 62.83% | 37.17% | 40.01% |
| BlockReceiptsPrefix | 800 | **100.00%** | 0% | 3.12% |

#### 關鍵觀察

- **SnapshotAccountPrefix** 的 98.81% 跨 SSTable 率最高，
  代表每次讀快照帳戶幾乎必然跳到新的 SSTable。
- **TrieNodeStoragePrefix / AccountPrefix** 各約 51–59%，
  略超過一半的 Trie node 讀取會跨 SSTable。
- **BlockReceiptsPrefix** 100% 跨 SSTable，但總數較少（800 次）。
- Cross-Level 與 Cross-SSTable 趨勢一致，確認兩者高度相關。

### Transition Matrix（前 4 列，顯示跨 category 的跳躍熱點）

```
from \ to              TrieNodeStorage  TrieNodeAccount  SnapshotAccount  SnapshotStorage
TrieNodeStoragePrefix    77,983           10,082            1,948            2,448
TrieNodeAccountPrefix     8,467           59,978            2,422            1,797
SnapshotAccountPrefix     2,554            1,648           13,144            5,758
SnapshotStoragePrefix     3,504            1,088            5,423            9,792
```

> 對角線（self-transition）數量大，代表同 category 內的連續 Get 仍有相當比例
> 落在同一 SSTable（locality 較高）。
> 非對角線的跨 category 跳躍則幾乎都是 cross-SSTable。

---

## 下一步

- [ ] 跑完整 95GB trace 取得最終統計數字
- [ ] 討論是否加入 per-block unique SSTable 數量作為補充指標
- [ ] 比較不同 state scheme（path-based vs hash-based）的 RA 差異
- [ ] 分析 RA 與 block 中 transaction 數量的相關性
