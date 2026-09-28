"""Per-OnToolUse syscall counts, base vs new, from syscount.sh's .counts files (N=5 timed calls)."""
import os
import sys

d = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__))
KEYS = ["fsync", "fdatasync", "openat", "read", "write", "renameat", "mkdirat", "newfstatat",
        "fstat", "close", "fcntl", "unlinkat"]
for mode in ["leased", "unleased"]:
    for fx in ["Deduped", "Delta", "AllNovel"]:
        res = {}
        for side in ["base", "new"]:
            c = {}
            with open(os.path.join(d, f"{fx}-{mode}-{side}.counts")) as f:
                for line in f:
                    p = line.split()
                    c[p[0]] = float(p[2])
            res[side] = c
        tot = {s: sum(res[s].get(k, 0) for k in KEYS) for s in res}
        print(f"\n{fx} {mode}: per OnToolUse; file syscalls counted below: "
              f"base {tot['base']:.1f} -> new {tot['new']:.1f}")
        for k in KEYS:
            b, n = res["base"].get(k, 0), res["new"].get(k, 0)
            if b or n:
                print(f"  {k:12s} base {b:8.1f}  new {n:8.1f}  delta {n - b:+8.1f}")
