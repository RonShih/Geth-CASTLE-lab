#!/usr/bin/env python3
"""
plotTrieNodeStats.py — Cumulative trie node stats per operation.

Usage:
  python3 plotTrieNodeStats.py <trie_node_stats.csv> <output_dir>

For each operation (get, insert, delete), generates one PNG with 4 subplots:
  1. Cumulative traversed node counts  (short, full, value, hash)
  2. Cumulative traversed node bytes    (short_bytes, full_bytes, value_bytes, hash_bytes)
  3. Cumulative resolved hashNode counts (resolved_short, resolved_full)
  4. Cumulative resolved hashNode bytes  (resolved_short_bytes, resolved_full_bytes)
Only plots sub-charts that have non-zero data.
"""

import csv
import os
import sys
from collections import defaultdict
from itertools import accumulate

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

# Sub-chart definitions: (title_suffix, y_label, columns)
SUBCHARTS = [
    ("traversed node counts", "count", [
        "traversed_short", "traversed_full", "traversed_value", "traversed_hash",
    ]),
    ("traversed node bytes", "bytes", [
        "traversed_short_bytes", "traversed_full_bytes",
        "traversed_value_bytes", "traversed_hash_bytes",
    ]),
    ("resolved hashNode counts", "count", [
        "resolved_short", "resolved_full",
    ]),
    ("resolved hashNode bytes", "bytes", [
        "resolved_short_bytes", "resolved_full_bytes",
    ]),
]

TARGET_OPS = ["get", "insert", "delete"]

DATA_COLS = [col for _, _, cols in SUBCHARTS for col in cols]


def read_csv(filepath):
    ops = defaultdict(lambda: defaultdict(list))
    with open(filepath, "r") as f:
        reader = csv.DictReader(f)
        for row in reader:
            if row["block_id"] == "TOTAL":
                continue
            op = row["op"]
            for key, val in row.items():
                if key == "op":
                    continue
                ops[op][key].append(int(val))
    return ops


def build_total(ops):
    """Sum get/insert/delete per block into a synthetic 'total' entry."""
    first_op = next((op for op in TARGET_OPS if op in ops), None)
    if first_op is None:
        return None
    blocks = ops[first_op]["block_id"]
    n = len(blocks)
    total = defaultdict(lambda: [0] * n)
    total["block_id"] = blocks
    for op in TARGET_OPS:
        if op not in ops:
            continue
        for col in DATA_COLS:
            if col in ops[op]:
                for i, v in enumerate(ops[op][col]):
                    total[col][i] += v
    return total


def has_data(values):
    return any(v != 0 for v in values)


def cumsum(values):
    return list(accumulate(values))


def plot_operation(op, data, output_dir):
    blocks = data["block_id"]

    # Filter to sub-charts that have non-zero data
    active = []
    for title_suffix, ylabel, cols in SUBCHARTS:
        active_cols = [c for c in cols if c in data and has_data(data[c])]
        if active_cols:
            active.append((title_suffix, ylabel, active_cols))

    if not active:
        return False

    n_plots = len(active)
    fig, axes = plt.subplots(n_plots, 1, figsize=(12, 4 * n_plots), sharex=True)
    if n_plots == 1:
        axes = [axes]

    for idx, (title_suffix, ylabel, cols) in enumerate(active):
        for col in cols:
            axes[idx].plot(blocks, cumsum(data[col]), linewidth=0.8, label=col)
        axes[idx].set_ylabel(ylabel)
        axes[idx].set_title(f"{op} — cumulative {title_suffix}")
        axes[idx].legend(loc="upper left", fontsize=8)
        axes[idx].grid(True, alpha=0.3)

    axes[-1].set_xlabel("block_id")
    fig.suptitle(f"Trie Node Stats (cumulative) — {op}", fontsize=14, fontweight="bold")
    fig.tight_layout()

    outpath = os.path.join(output_dir, f"trie_stats_{op}.png")
    fig.savefig(outpath, dpi=150)
    plt.close(fig)
    print(f"  Saved: {outpath}")
    return True


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <trie_node_stats.csv> <output_dir>")
        sys.exit(1)

    csv_path = sys.argv[1]
    output_dir = sys.argv[2]
    os.makedirs(output_dir, exist_ok=True)

    ops = read_csv(csv_path)
    plotted = 0
    for op in TARGET_OPS:
        if op not in ops:
            continue
        if plot_operation(op, ops[op], output_dir):
            plotted += 1

    # Generate total (sum of all ops per block)
    total = build_total(ops)
    if total is not None and plot_operation("total", total, output_dir):
        plotted += 1

    if plotted == 0:
        print("  No non-zero data found, no plots generated.")
    else:
        print(f"  {plotted} plot(s) generated in {output_dir}")


if __name__ == "__main__":
    main()
