"""Explain how two qompack binaries differ: size, differing byte ranges, and version strings.

usage: python bindiff.py <frozen-binary> <candidate-binary>
"""
import re
import sys


def ranges(a, b):
    out = []
    n = min(len(a), len(b))
    i = 0
    while i < n:
        if a[i] != b[i]:
            j = i
            while j < n and a[j] != b[j]:
                j += 1
            out.append((i, j))
            i = j
        else:
            i += 1
    return out


def ctx(buf, lo, hi, pad=24):
    s = buf[max(0, lo - pad):hi + pad]
    return re.sub(rb"[^\x20-\x7e]", b".", s).decode()


def main():
    a = open(sys.argv[1], "rb").read()
    b = open(sys.argv[2], "rb").read()
    print(f"sizes: frozen={len(a)} candidate={len(b)}")
    rs = ranges(a, b)
    print(f"differing ranges: {len(rs)}, differing bytes: {sum(j - i for i, j in rs)}")
    for i, j in rs[:20]:
        print(f"  @0x{i:x}+{j - i}: frozen[{ctx(a, i, j)}] candidate[{ctx(b, i, j)}]")
    for label, buf in (("frozen", a), ("candidate", b)):
        for pat in (rb"0\.1\.0", rb"0\.3\.0"):
            hits = [m.start() for m in re.finditer(pat, buf)]
            print(f"{label}: {pat.decode()} at {len(hits)} offset(s): "
                  + ", ".join(f"0x{h:x}[{ctx(buf, h, h + 5, 12)}]" for h in hits[:12]))


if __name__ == "__main__":
    main()
