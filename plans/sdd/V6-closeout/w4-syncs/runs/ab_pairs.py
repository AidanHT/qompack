"""Paired A/B summary for w4-syncs: per-round new/base ratios and an exact two-sided sign test.

usage: python ab_pairs.py <dir-with-base.txt-and-new.txt>

Each side file holds one block per round, opened by '# round=R side=S start=...' and closed by
'# round=R side=S exit=E ...'. A block whose exit is not 0 is dropped with its partner. Samples are
paired by (round, benchmark), so a pair always shares the host's load window. Absolute values are
co-loaded and are NOT budget evidence; only the paired ratios and the sign test are claimed.
"""

import math
import re
import statistics
import sys
from collections import defaultdict

ROUND = re.compile(r"^# round=(\d+) side=(\w+) (start|exit)=(\S+)")
BENCH = re.compile(r"^(Benchmark\S+?)(-\d+)?\s+(\d+)\s+(.*)$")
METRICS = ("ns/op", "p50-ms", "p99-ms", "B/op", "allocs/op")


def parse(path):
    rounds = defaultdict(dict)
    exits = {}
    cur = None
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            m = ROUND.match(line)
            if m:
                r, kind, val = int(m.group(1)), m.group(3), m.group(4)
                if kind == "start":
                    cur = r
                else:
                    exits[r] = val
                    cur = None
                continue
            m = BENCH.match(line)
            if m and cur is not None:
                fields = m.group(4).split()
                vals = {}
                for i in range(0, len(fields) - 1, 2):
                    try:
                        vals[fields[i + 1]] = float(fields[i])
                    except ValueError:
                        pass
                rounds[cur][m.group(1)] = vals
    return {r: v for r, v in rounds.items() if exits.get(r) == "0"}, exits


def sign_p(k, n):
    """Exact two-sided sign-test p for k successes of n."""
    if n == 0:
        return float("nan")
    tail = sum(math.comb(n, i) for i in range(0, min(k, n - k) + 1)) / 2**n
    return min(1.0, 2 * tail)


def main():
    d = sys.argv[1]
    base, bex = parse(d + "/base.txt")
    new, nex = parse(d + "/new.txt")
    bad = sorted(r for r in set(bex) | set(nex) if bex.get(r) != "0" or nex.get(r) != "0")
    rounds = sorted(set(base) & set(new))
    print(f"paired rounds: {len(rounds)} ({rounds[0] if rounds else '-'}..{rounds[-1] if rounds else '-'})"
          f"; dropped rounds (non-zero exit on either side): {bad or 'none'}")
    names = sorted({n for r in rounds for n in base[r]} & {n for r in rounds for n in new[r]})
    for metric in METRICS:
        print(f"\n## {metric}  (median base -> median new; median per-round ratio new/base; "
              f"rounds new<base; exact two-sided sign test)")
        for name in names:
            pairs = [(base[r][name][metric], new[r][name][metric]) for r in rounds
                     if name in base[r] and name in new[r]
                     and metric in base[r][name] and metric in new[r][name]]
            if not pairs:
                continue
            ratios = [n / b for b, n in pairs if b > 0]
            lower = sum(1 for b, n in pairs if n < b)
            ties = sum(1 for b, n in pairs if n == b)
            n_eff = len(pairs) - ties
            p = sign_p(lower, n_eff)
            mb = statistics.median(b for b, _ in pairs)
            mn = statistics.median(n for _, n in pairs)
            mr = statistics.median(ratios) if ratios else float("nan")
            print(f"{name:52s} n={len(pairs):2d}  {mb:14.4g} -> {mn:14.4g}  ratio {mr:6.3f}"
                  f" ({(mr - 1) * 100:+6.1f}%)  lower {lower}/{n_eff}  p={p:.4f}")


if __name__ == "__main__":
    main()
