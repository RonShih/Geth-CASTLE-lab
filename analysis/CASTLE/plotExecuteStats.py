#!/usr/bin/env python3
"""
plotExecuteStats.py — Visualize per-block ExecuteStats timing breakdown.

Usage:
  python3 plotExecuteStats.py <execute_stats.csv> <output_dir> [bucket_size]

  bucket_size: aggregate every N blocks into one bar (default: auto-select
               so that there are ~200 bars).  Use 1 to disable aggregation.

Generates:
  1. Stacked bar chart: processing / validation / write phase per block
  2. Stacked bar chart: sub-component breakdown per phase (3 sub-plots)
  3. Stacked area chart: total time with phase overlay
  4. Summary text file with averages and totals
"""

import csv
import os
import sys

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker


def read_csv(filepath):
    rows = []
    with open(filepath, "r") as f:
        reader = csv.DictReader(f)
        for row in reader:
            parsed = {}
            for k, v in row.items():
                k = k.strip()
                if k == "block_id":
                    parsed[k] = v.strip()
                else:
                    parsed[k] = int(v)
            rows.append(parsed)
    return rows


def us_to_ms(val):
    """Convert microseconds to milliseconds."""
    return val / 1000.0


def auto_bucket_size(n, target_bars=200):
    """Pick a bucket size so we get roughly target_bars bars."""
    if n <= target_bars:
        return 1
    # Round to a "nice" number
    raw = n / target_bars
    for nice in [1, 2, 5, 10, 20, 50, 100, 200, 500,
                 1000, 2000, 5000, 10000, 20000, 50000, 100000]:
        if nice >= raw:
            return nice
    return int(raw)


def aggregate_rows(rows, bucket_size):
    """Aggregate rows into buckets of bucket_size, averaging numeric fields."""
    if bucket_size <= 1:
        return rows, [r["block_id"] for r in rows]

    numeric_keys = [k for k in rows[0] if k != "block_id"]
    buckets = []
    labels = []

    for i in range(0, len(rows), bucket_size):
        chunk = rows[i:i + bucket_size]
        n = len(chunk)
        agg = {}
        for k in numeric_keys:
            agg[k] = sum(r[k] for r in chunk) / n  # average
        agg["block_id"] = f"{chunk[0]['block_id']}~{chunk[-1]['block_id']}"
        buckets.append(agg)
        labels.append(agg["block_id"])

    return buckets, labels


def make_xticks(labels, max_labels=25):
    """Return (tick_positions, tick_labels) with at most max_labels shown."""
    n = len(labels)
    step = max(1, n // max_labels)
    positions = [i for i in range(n) if i % step == 0]
    tick_labels = [labels[i] for i in positions]
    return positions, tick_labels


def plot_phase_overview(rows, labels, output_dir, bucket_size):
    """Stacked bar: ptime / vtime / wtime per block (or bucket)."""
    ptime = [us_to_ms(r["ptime_us"]) for r in rows]
    vtime = [us_to_ms(r["vtime_us"]) for r in rows]
    wtime = [us_to_ms(r["wtime_us"]) for r in rows]

    x = range(len(rows))
    xticks, xlabels = make_xticks(labels)

    fig, ax = plt.subplots(figsize=(14, 5))
    ax.bar(x, ptime, label="Processing (ptime)", color="#4C72B0", width=1.0)
    ax.bar(x, vtime, bottom=ptime, label="Validation (vtime)", color="#55A868", width=1.0)
    ax.bar(x, wtime, bottom=[p + v for p, v in zip(ptime, vtime)], label="Write (wtime)", color="#C44E52", width=1.0)

    ax.set_xlabel("Block ID")
    ylabel = "Avg Time (ms)" if bucket_size > 1 else "Time (ms)"
    ax.set_ylabel(ylabel)
    title = "insertChain Phase Breakdown per Block"
    if bucket_size > 1:
        title += f" (avg per {bucket_size} blocks)"
    ax.set_title(title)
    ax.legend(loc="upper left")
    ax.grid(True, alpha=0.3, axis="y")
    ax.set_xticks(xticks)
    ax.set_xticklabels(xlabels, rotation=45, ha="right", fontsize=7)

    fig.tight_layout()
    path = os.path.join(output_dir, "execute_stats_phases.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"  Saved: {path}")


def plot_detailed_breakdown(rows, labels, output_dir, bucket_size):
    """Three sub-plots showing component breakdown within each phase."""
    x = range(len(rows))
    xticks, xlabels = make_xticks(labels)

    fig, axes = plt.subplots(3, 1, figsize=(14, 12), sharex=True)

    ylabel = "Avg Time (ms)" if bucket_size > 1 else "Time (ms)"

    # --- Processing breakdown ---
    ax = axes[0]
    components = [
        ("execution_us", "Execution", "#4C72B0"),
        ("account_reads_us", "AccountReads", "#8DA0CB"),
        ("storage_reads_us", "StorageReads", "#66C2A5"),
        ("code_reads_us", "CodeReads", "#FC8D62"),
    ]
    bottom = [0.0] * len(rows)
    for col, label, color in components:
        vals = [us_to_ms(r[col]) for r in rows]
        ax.bar(x, vals, bottom=bottom, label=label, color=color, width=1.0)
        bottom = [b + v for b, v in zip(bottom, vals)]
    ptime = [us_to_ms(r["ptime_us"]) for r in rows]
    ax.plot(x, ptime, color="black", linewidth=0.8, linestyle="--", label="ptime (total)")
    ax.set_ylabel(ylabel)
    ax.set_title("Processing Phase Breakdown")
    ax.legend(loc="upper left", fontsize=8)
    ax.grid(True, alpha=0.3, axis="y")

    # --- Validation breakdown ---
    ax = axes[1]
    components = [
        ("validation_us", "Validation", "#55A868"),
        ("account_hashes_us", "AccountHashes", "#B2DF8A"),
        ("account_updates_us", "AccountUpdates", "#33A02C"),
        ("storage_updates_us", "StorageUpdates", "#A6CEE3"),
    ]
    bottom = [0.0] * len(rows)
    for col, label, color in components:
        vals = [us_to_ms(r[col]) for r in rows]
        ax.bar(x, vals, bottom=bottom, label=label, color=color, width=1.0)
        bottom = [b + v for b, v in zip(bottom, vals)]
    vtime = [us_to_ms(r["vtime_us"]) for r in rows]
    ax.plot(x, vtime, color="black", linewidth=0.8, linestyle="--", label="vtime (total)")
    ax.set_ylabel(ylabel)
    ax.set_title("Validation Phase Breakdown")
    ax.legend(loc="upper left", fontsize=8)
    ax.grid(True, alpha=0.3, axis="y")

    # --- Write breakdown ---
    ax = axes[2]
    components = [
        ("account_commits_us", "AccountCommits", "#C44E52"),
        ("storage_commits_us", "StorageCommits", "#E78AC3"),
        ("snapshot_commit_us", "SnapshotCommit", "#FFD92F"),
        ("triedb_commit_us", "TrieDBCommit", "#A65628"),
        ("block_write_us", "BlockWrite", "#984EA3"),
    ]
    bottom = [0.0] * len(rows)
    for col, label, color in components:
        vals = [us_to_ms(r[col]) for r in rows]
        ax.bar(x, vals, bottom=bottom, label=label, color=color, width=1.0)
        bottom = [b + v for b, v in zip(bottom, vals)]
    wtime = [us_to_ms(r["wtime_us"]) for r in rows]
    ax.plot(x, wtime, color="black", linewidth=0.8, linestyle="--", label="wtime (total)")
    ax.set_ylabel(ylabel)
    ax.set_title("Write Phase Breakdown")
    ax.legend(loc="upper left", fontsize=8)
    ax.grid(True, alpha=0.3, axis="y")

    ax.set_xlabel("Block ID")
    ax.set_xticks(xticks)
    ax.set_xticklabels(xlabels, rotation=45, ha="right", fontsize=7)

    suptitle = "ExecuteStats Detailed Breakdown per Block"
    if bucket_size > 1:
        suptitle += f" (avg per {bucket_size} blocks)"
    fig.suptitle(suptitle, fontsize=14, fontweight="bold")
    fig.tight_layout()
    path = os.path.join(output_dir, "execute_stats_breakdown.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"  Saved: {path}")


def plot_total_time(rows, labels, output_dir, bucket_size):
    """Line chart of total_time per block with ptime/vtime/wtime stacked area."""
    x = list(range(len(rows)))

    ptime = [us_to_ms(r["ptime_us"]) for r in rows]
    vtime = [us_to_ms(r["vtime_us"]) for r in rows]
    wtime = [us_to_ms(r["wtime_us"]) for r in rows]
    total = [us_to_ms(r["total_time_us"]) for r in rows]

    xticks, xlabels = make_xticks(labels)

    fig, ax = plt.subplots(figsize=(14, 5))
    ax.stackplot(x, ptime, vtime, wtime,
                 labels=["Processing", "Validation", "Write"],
                 colors=["#4C72B0", "#55A868", "#C44E52"], alpha=0.7)
    ax.plot(x, total, color="black", linewidth=1.0, linestyle="-", label="TotalTime")
    ax.set_xlabel("Block ID")
    ylabel = "Avg Time (ms)" if bucket_size > 1 else "Time (ms)"
    ax.set_ylabel(ylabel)
    title = "Total Block Execution Time"
    if bucket_size > 1:
        title += f" (avg per {bucket_size} blocks)"
    ax.set_title(title)
    ax.legend(loc="upper left")
    ax.grid(True, alpha=0.3)
    ax.set_xticks(xticks)
    ax.set_xticklabels([labels[i] for i in xticks], rotation=45, ha="right", fontsize=7)

    fig.tight_layout()
    path = os.path.join(output_dir, "execute_stats_total.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    print(f"  Saved: {path}")


def write_summary(rows_raw, output_dir):
    """Write a text summary with averages and totals (always uses raw data)."""
    path = os.path.join(output_dir, "execute_stats_summary.txt")
    n = len(rows_raw)
    if n == 0:
        return

    cols = [
        "execution_us", "account_reads_us", "storage_reads_us", "code_reads_us", "ptime_us",
        "validation_us", "account_hashes_us", "account_updates_us", "storage_updates_us", "vtime_us",
        "account_commits_us", "storage_commits_us", "snapshot_commit_us", "triedb_commit_us", "block_write_us", "wtime_us",
        "total_time_us",
    ]
    phase_labels = {
        "execution_us": "  Execution",
        "account_reads_us": "  AccountReads",
        "storage_reads_us": "  StorageReads",
        "code_reads_us": "  CodeReads",
        "ptime_us": "  ptime (total)",
        "validation_us": "  Validation",
        "account_hashes_us": "  AccountHashes",
        "account_updates_us": "  AccountUpdates",
        "storage_updates_us": "  StorageUpdates",
        "vtime_us": "  vtime (total)",
        "account_commits_us": "  AccountCommits",
        "storage_commits_us": "  StorageCommits",
        "snapshot_commit_us": "  SnapshotCommit",
        "triedb_commit_us": "  TrieDBCommit",
        "block_write_us": "  BlockWrite",
        "wtime_us": "  wtime (total)",
        "total_time_us": "TotalTime",
    }

    with open(path, "w") as f:
        f.write(f"=== ExecuteStats Summary ({n} blocks) ===\n")
        f.write(f"Block range: {rows_raw[0]['block_id']} ~ {rows_raw[-1]['block_id']}\n\n")

        f.write(f"{'Phase':<25s} {'Total (ms)':>14s} {'Avg (ms)':>12s} {'Min (ms)':>12s} {'Max (ms)':>12s} {'% of TotalTime':>14s}\n")
        f.write("-" * 81 + "\n")

        total_sum = sum(r["total_time_us"] for r in rows_raw)

        section_headers = {
            "execution_us": "\n[Processing]",
            "validation_us": "\n[Validation]",
            "account_commits_us": "\n[Write]",
            "total_time_us": "\n[Overall]",
        }

        for col in cols:
            if col in section_headers:
                f.write(section_headers[col] + "\n")

            vals = [r[col] for r in rows_raw]
            s = sum(vals)
            avg = s / n
            mn = min(vals)
            mx = max(vals)
            pct = 100.0 * s / total_sum if total_sum > 0 else 0.0

            label = phase_labels.get(col, col)
            f.write(f"{label:<25s} {us_to_ms(s):>14.2f} {us_to_ms(avg):>12.2f} {us_to_ms(mn):>12.2f} {us_to_ms(mx):>12.2f} {pct:>13.2f}%\n")

    print(f"  Saved: {path}")


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <execute_stats.csv> <output_dir> [bucket_size]")
        print(f"  bucket_size: aggregate every N blocks (default: auto)")
        sys.exit(1)

    csv_path = sys.argv[1]
    output_dir = sys.argv[2]
    os.makedirs(output_dir, exist_ok=True)

    rows_raw = read_csv(csv_path)
    if not rows_raw:
        print("  No data found in CSV.")
        return

    # Determine bucket size
    if len(sys.argv) >= 4:
        bucket_size = int(sys.argv[3])
    else:
        bucket_size = auto_bucket_size(len(rows_raw))

    print(f"  Read {len(rows_raw)} blocks from {csv_path}")
    if bucket_size > 1:
        print(f"  Aggregating every {bucket_size} blocks → {(len(rows_raw) + bucket_size - 1) // bucket_size} bars")

    rows_agg, labels = aggregate_rows(rows_raw, bucket_size)

    plot_phase_overview(rows_agg, labels, output_dir, bucket_size)
    plot_detailed_breakdown(rows_agg, labels, output_dir, bucket_size)
    plot_total_time(rows_agg, labels, output_dir, bucket_size)
    write_summary(rows_raw, output_dir)  # summary always uses raw data
    print(f"  Done. {4} outputs generated in {output_dir}")


if __name__ == "__main__":
    main()
