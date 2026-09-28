"""Paired process-CPU A/B rows from a *-cpu.tsv written by win-cpu-ab.ps1 or linux-cpu-ab.sh.

usage: cpu_table.py <tsv> <baseName> <finalName> <platform> <label> <ops_per_run>
ops_per_run is how many benchmark iterations one process ran (run1's single iteration plus the
fixed -benchtime count), so CPU per op = (user+kernel) / ops_per_run, setup included.
"""
import math, sys
from math import comb
path, bn, fn, plat, label, ops = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5], float(sys.argv[6])
rows = {}
for line in open(path, encoding='utf-8-sig'):
    parts = line.rstrip('\n').split('\t')
    if len(parts) < 6 or parts[0] in ('round', 'done'):
        continue
    rnd, v = parts[0], parts[1]
    wall, user, kern = float(parts[2]), float(parts[3]), float(parts[4])
    rows.setdefault(rnd, {})[v] = (wall, user + kern)
pairs = [(r[bn], r[fn]) for r in rows.values() if bn in r and fn in r]
def med(xs):
    s = sorted(xs); n = len(s)
    return s[n//2] if n % 2 else (s[n//2-1] + s[n//2]) / 2
def fmt(sec):
    us = sec * 1e6
    return f'{us/1000:.2f} ms' if us >= 1000 else f'{us:.1f} µs'
for idx, name in ((1, 'CPU (user+kernel)'), (0, 'wall')):
    b = [p[0][idx] / ops for p in pairs]; f = [p[1][idx] / ops for p in pairs]
    ratios = [y / x for x, y in zip(b, f) if x > 0]
    gm = math.exp(sum(math.log(r) for r in ratios) / len(ratios))
    wins = sum(1 for r in ratios if r < 1); n = len(ratios)
    k = min(wins, n - wins); p = min(1.0, 2 * sum(comb(n, j) for j in range(k + 1)) / 2 ** n)
    print(f'| {plat} | {label} | {name} per op | {n} | {fmt(min(b))} / **{fmt(med(b))}** / {fmt(max(b))} | '
          f'{fmt(min(f))} / **{fmt(med(f))}** / {fmt(max(f))} | {(gm-1)*100:+.0f}% ({min(ratios):.2f}–{max(ratios):.2f}) | {wins}/{n}, p={p:.3f} |')
