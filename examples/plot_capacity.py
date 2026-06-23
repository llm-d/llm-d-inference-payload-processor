"""
Plot effective TTFT vs real TTFT from a ttft-percentile extractor log file.

Produces one file with 4 panels:
  1. effectiveTTFT line + real TTFT scatter
  2. effectiveTTFT line + real TTFT binned median line
  3. Inflight requests
  4. P10Low and P50 TTFT lines

Usage: python plot_capacity.py <log_file>
"""

import json
import statistics
import sys
from collections import defaultdict

import matplotlib.pyplot as plt

LOG_FILE  = sys.argv[1] if len(sys.argv) > 1 else "logs/bench_median33"
MAX_T_SEC = float(sys.argv[2]) if len(sys.argv) > 2 else None  # optional time cutoff
BIN_SEC   = 10  # bucket width for the median graph

# ---------------------------------------------------------------------------
# Parse
# ---------------------------------------------------------------------------
flush_records = defaultdict(list)
obs_records   = defaultdict(list)

with open(LOG_FILE) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        msg   = obj.get("msg", "")
        model = obj.get("model", "")
        if not model:
            continue
        if msg == "ttft-percentile wrote attribute":
            flush_records[model].append({
                "ts":              obj["ts"],
                "inflight":        obj.get("Requests", 0),
                "inflight_at_p50": obj.get("InflightAtP50", 0),
                "p10low":          obj.get("P10Low_s", 0),
                "p50":             obj.get("P50_s", 0),
                "effective":       obj.get("EffectiveTTFT_s", 0),
            })
        elif msg == "ttft-observation":
            ttft = obj.get("ttft_s", 0)
            obs_records[model].append({
                "ts":   obj["ts"] - ttft,  # align to dispatch time (response_ts - ttft)
                "ttft": ttft,
            })

if not flush_records:
    print("No 'ttft-percentile wrote attribute' lines found in", LOG_FILE)
    sys.exit(1)

# Normalise timestamps
t0 = min(
    r["ts"]
    for recs in list(flush_records.values()) + list(obs_records.values())
    for r in recs
)
for recs in flush_records.values():
    for r in recs:
        r["t"] = r["ts"] - t0
for recs in obs_records.values():
    for r in recs:
        r["t"] = r["ts"] - t0

if MAX_T_SEC is not None:
    flush_records = {m: [r for r in recs if r["t"] <= MAX_T_SEC] for m, recs in flush_records.items()}
    obs_records   = {m: [r for r in recs if r["t"] <= MAX_T_SEC] for m, recs in obs_records.items()}

models = sorted(flush_records.keys())
colors = ["steelblue", "tomato", "seagreen", "darkorange"]
base   = LOG_FILE.rstrip("/").split("/")[-1]

fig, axes = plt.subplots(6, 1, figsize=(14, 20), sharex=True)
fig.suptitle(f"Effective TTFT vs Real TTFT — {base}", fontsize=13)
ax_scatter, ax_median, ax_inflight, ax_ttft_stat, ax_load_ratio, ax_spread = axes

for i, model in enumerate(models):
    color = colors[i % len(colors)]
    label = model.split("/")[-1]
    frecs = flush_records[model]
    orecs = obs_records.get(model, [])
    ft     = [r["t"]              for r in frecs]
    fe     = [r["effective"]      for r in frecs]
    fp     = [r["p10low"]         for r in frecs]
    fp50   = [r["p50"]            for r in frecs]
    fi     = [r["inflight"]       for r in frecs]
    fip50  = [r["inflight_at_p50"] for r in frecs]
    # load ratio: inflight / inflightAtP50 (capped to avoid div-by-zero noise)
    fratio = [inf / ip50 if ip50 > 0 else 0 for inf, ip50 in zip(fi, fip50)]
    # spread: P50 - P10Low (the TTFT range the formula scales over)
    fspread = [p50 - p10 for p50, p10 in zip(fp50, fp)]

    # Panel 1: scatter
    ax_scatter.plot(ft, fe, color=color, linewidth=1.2, alpha=0.9,
                    linestyle="-", label=f"{label} effectiveTTFT")
    if orecs:
        ot = [r["t"]    for r in orecs]
        ov = [r["ttft"] for r in orecs]
        ax_scatter.scatter(ot, ov, color=color, alpha=0.15, s=3,
                           label=f"{label} real TTFT")

    # Panel 2: binned median line
    ax_median.plot(ft, fe, color=color, linewidth=1.2, alpha=0.9,
                   linestyle="-", label=f"{label} effectiveTTFT")
    if orecs:
        bins = defaultdict(list)
        for r in orecs:
            bins[int(r["t"] // BIN_SEC)].append(r["ttft"])
        bt = sorted(bins)
        bv = [statistics.median(bins[b]) for b in bt]
        ax_median.plot([b * BIN_SEC + BIN_SEC / 2 for b in bt], bv,
                       color=color, linewidth=1.2, alpha=0.7,
                       linestyle="--", label=f"{label} real TTFT (median/{BIN_SEC}s)")

    # Panel 3: inflight
    ax_inflight.plot(ft, fi, color=color, alpha=0.7, linewidth=0.8, label=label)

    # Panel 4: P10Low and P50
    ax_ttft_stat.plot(ft, fp,   color=color, alpha=0.9, linewidth=1.0,
                      linestyle="-",  label=f"{label} P10Low")
    ax_ttft_stat.plot(ft, fp50, color=color, alpha=0.5, linewidth=0.8,
                      linestyle="--", label=f"{label} P50")

    # Panel 5: load ratio = inflight / inflightAtP50
    ax_load_ratio.plot(ft, fratio, color=color, linewidth=1.0, alpha=0.85, label=label)
    ax_load_ratio.axhline(1.0, color="gray", linewidth=0.7, linestyle=":")

    # Panel 6: spread = P50 - P10Low
    ax_spread.plot(ft, fspread, color=color, linewidth=1.0, alpha=0.85, label=label)

for ax, title, ylabel in [
    (ax_scatter,    "effectiveTTFT vs real TTFT (scatter)",              "TTFT (s)"),
    (ax_median,     f"effectiveTTFT vs real TTFT (median/{BIN_SEC}s bins)", "TTFT (s)"),
    (ax_inflight,   "Inflight requests",                                 "Inflight"),
    (ax_ttft_stat,  "P10Low and P50",                                    "TTFT (s)"),
    (ax_load_ratio, "Load ratio: inflight / inflightAtP50  (1.0 = median load)", "ratio"),
    (ax_spread,     "Spread: P50 − P10Low  (TTFT range the slope scales over)", "TTFT (s)"),
]:
    ax.set_ylabel(ylabel)
    ax.set_ylim(bottom=0)
    ax.legend(fontsize=8, ncol=2)
    ax.grid(True, alpha=0.3)
    ax.set_title(title, fontsize=9, loc="left", pad=3)

ax_ttft_stat.set_xlabel("Time since first request (s)")

plt.tight_layout()
out = base + "_ttft_cmp.png"
fig.savefig(out, dpi=150)
print("saved to", out)
plt.show()
