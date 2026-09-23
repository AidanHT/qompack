"""Markdown rows for the perfstore report from interleaved A/B raw rounds.

usage: table.py <dir> <label> <rounds> <baseName> <headName> <platform>
Per benchmark: n pairs, base and final distributions (min / median / max ns/op), the paired
geometric-mean ratio with its range, head-faster count and exact two-sided sign-test p, and the
allocs/op and B/op medians.
"""
import math, re, sys
from math import comb

d, label, rounds, bn, hn, plat = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4], sys.argv[5], sys.argv[6]
pat = re.compile(r'^(Benchmark\S+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op(?:\s+[\d.]+ MB/s)?(?:\s+([\d.]+) B/op\s+([\d.]+) allocs/op)?')

def load(v, i):
    out = {}
    try:
        for line in open(f'{d}/{label}-{v}-round{i}.raw', encoding='utf-8', errors='replace'):
            m = pat.match(line)
            if m:
                out[m.group(1)] = (float(m.group(3)), float(m.group(4) or 'nan'), float(m.group(5) or 'nan'))
    except FileNotFoundError:
        pass
    return out

def fmt(ns):
    if ns >= 1e6: return f'{ns/1e6:.2f} ms'
    return f'{ns/1e3:.1f} µs'

def med(xs):
    s = sorted(xs); n = len(s)
    return s[n//2] if n % 2 else (s[n//2-1] + s[n//2]) / 2

pairs = {}
for i in range(1, rounds + 1):
    a, b = load(bn, i), load(hn, i)
    for k in a:
        if k in b:
            pairs.setdefault(k, []).append((a[k], b[k]))
for k, ps in pairs.items():
    base = [p[0][0] for p in ps]; head = [p[1][0] for p in ps]
    ratios = [h / b for b, h in zip(base, head)]
    gm = math.exp(sum(math.log(r) for r in ratios) / len(ratios))
    wins = sum(1 for r in ratios if r < 1); n = len(ps)
    kk = min(wins, n - wins); p = min(1.0, 2 * sum(comb(n, j) for j in range(kk + 1)) / 2 ** n)
    ba = med([x[0][2] for x in ps]); ha = med([x[1][2] for x in ps])
    bb = med([x[0][1] for x in ps]); hb = med([x[1][1] for x in ps])
    name = k.replace('Benchmark', '')
    print(f'| {plat} | {name} | {n} | {fmt(min(base))} / **{fmt(med(base))}** / {fmt(max(base))} | '
          f'{fmt(min(head))} / **{fmt(med(head))}** / {fmt(max(head))} | {(gm-1)*100:+.0f}% '
          f'({min(ratios):.2f}–{max(ratios):.2f}) | {wins}/{n}, p={p:.3f} | {ba:.0f} → {ha:.0f} | '
          f'{bb/1024:.1f} → {hb/1024:.1f} KiB |')
