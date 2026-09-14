#!/usr/bin/env python3
"""turnmetrics — CS-7 measurement script (HARVEST_PATH_SPEC).

Parses an AII OS operational log for TURN_SUMMARY lines and emits
per-day numbers: total turns, breadth-shaped turns (read_only >= 2 —
two or more independent read-only subgoals in one turn), spawn count,
harvested count. Reports harvest-wake count when CS-1 is present;
reports n/a otherwise.

The WAKE counter matches only successful wakes ("woke and spoke") —
gate-dropouts and failed wakes log the same HARVEST_WAKE prefix but
are different events (a coalesced delivery never woke the resident at
all), and counting them would inflate the wake metric with exactly
the turns that did not happen. HARVEST_SWEEP lines (the compose-time
count of outcomes folded into working state) are counted separately
as sweeps.

Usage:
    turnmetrics.py [logfile ...]        # or stdin when no files given
"""
import re
import sys
from collections import defaultdict

# Default stdlib log prefix: 2004/01/02 15:04:05 — the date is the
# line's first 10 chars.
TS = r"^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}"
TURN = re.compile(TS + r" TURN_SUMMARY calls=(\d+) read_only=(\d+) spawned=(\d+) harvested=(\d+)")
WAKE = re.compile(TS + r" HARVEST_WAKE \S+: woke and spoke")
SWEEP = re.compile(TS + r" HARVEST_SWEEP swept=(\d+)")

def parse(lines):
    days = defaultdict(lambda: {"turns": 0, "breadth": 0, "spawned": 0,
                                 "harvested": 0, "sweeps": 0, "swept_n": 0})
    wakes = 0
    for line in lines:
        m = TURN.match(line)
        if m:
            calls, ro, sp, hv = map(int, m.groups())
            d = line[:10]
            v = days[d]
            v["turns"] += 1
            if ro >= 2:
                v["breadth"] += 1
            v["spawned"] += sp
            v["harvested"] += hv
            continue
        m = SWEEP.match(line)
        if m:
            v = days[line[:10]]
            v["sweeps"] += 1
            v["swept_n"] += int(m.group(1))
            continue
        if WAKE.match(line):
            wakes += 1
    return days, wakes

def main(argv):
    files = argv[1:]
    if files:
        lines = []
        for f in files:
            with open(f, errors="replace") as fh:
                lines.extend(fh)
    else:
        lines = sys.stdin
    days, wakes = parse(lines)
    if not days:
        print("no TURN_SUMMARY lines found")
        return 1
    print(f"{'date':<12} {'turns':>6} {'breadth':>8} {'spawned':>8} {'harvested':>10} {'swept':>6}")
    for d in sorted(days):
        v = days[d]
        print(f"{d:<12} {v['turns']:>6} {v['breadth']:>8} {v['spawned']:>8} {v['harvested']:>10} {v['swept_n']:>6}")
    if wakes:
        print(f"harvest wakes (successful): {wakes}")
    else:
        print("harvest latency: n/a (no HARVEST_WAKE 'woke and spoke' lines — CS-1 not built or not fired)")
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv))
