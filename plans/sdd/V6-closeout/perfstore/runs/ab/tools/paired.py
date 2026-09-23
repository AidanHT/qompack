"""Paired A/B analysis of interleaved benchmark rounds.

usage: paired.py <dir> <label> <rounds> [baseName headName]
Reads <dir>/<label>-<v>-round<i>.raw for v in (base, head) and prints, per benchmark, the per-round
head/base ns/op ratio, the geometric mean ratio, the median ratio, how many rounds head was faster,
and an exact two-sided sign-test p-value. Pairs are adjacent in time (ABBA order), so co-load drift
between rounds cancels in the ratio.
"""
import math, re, sys
from math import comb

d, label, rounds = sys.argv[1], sys.argv[2], int(sys.argv[3])
names = sys.argv[4:6] if len(sys.argv) > 5 else ['base', 'head']
pat = re.compile(r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op(?:.*?\s([\d.]+) B/op\s+([\d.]+) allocs/op)?')

def load(v, i):
    out = {}
    try:
        for line in open(f'{d}/{label}-{v}-round{i}.raw', encoding='utf-8', errors='replace'):
            m = pat.match(line)
            if m:
                out[m.group(1)] = (float(m.group(2)), m.group(3), m.group(4))
    except FileNotFoundError:
        pass
    return out

pairs = {}
for i in range(1, rounds + 1):
    a, b = load(names[0], i), load(names[1], i)
    for k in a:
        if k in b:
            pairs.setdefault(k, []).append((a[k], b[k]))

def sign_p(wins, n):
    k = min(wins, n - wins)
    return min(1.0, 2 * sum(comb(n, j) for j in range(k + 1)) / 2 ** n)

for k, ps in pairs.items():
    ratios = [h[0] / b[0] for b, h in ps]
    gm = math.exp(sum(math.log(r) for r in ratios) / len(ratios))
    med = sorted(ratios)[len(ratios) // 2] if len(ratios) % 2 else sum(sorted(ratios)[len(ratios)//2-1:len(ratios)//2+1]) / 2
    wins = sum(1 for r in ratios if r < 1)
    bmed = sorted(b[0] for b, _ in ps)[len(ps) // 2]
    hmed = sorted(h[0] for _, h in ps)[len(ps) // 2]
    ballocs = ps[0][0][2]; hallocs = ps[0][1][2]
    print(f'{k}: n={len(ps)} base_med={bmed/1e3:.1f}us head_med={hmed/1e3:.1f}us '
          f'paired_geomean_ratio={gm:.3f} ({(gm-1)*100:+.1f}%) median_ratio={med:.3f} '
          f'head_faster={wins}/{len(ps)} sign_p={sign_p(wins, len(ps)):.4f} allocs {ballocs}->{hallocs}')
    print('   ratios: ' + ' '.join(f'{r:.2f}' for r in ratios))
