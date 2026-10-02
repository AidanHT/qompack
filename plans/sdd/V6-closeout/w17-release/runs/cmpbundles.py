"""Compare two `devtool bundle --archive` output trees file by file and zip member by member.

usage: python cmpbundles.py <frozen-dir> <candidate-dir>

Prints one line per file (sha256 equal or DIFF), then for every archive the member-level
comparison (name, mode, timestamp, CRC, bytes), so a differing zip is explained by the member that
differs rather than only by its digest.
"""
import hashlib
import json
import os
import sys
import zipfile


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def walk(root):
    out = {}
    for dp, _, fns in os.walk(root):
        for fn in fns:
            p = os.path.join(dp, fn)
            out[os.path.relpath(p, root).replace(os.sep, "/")] = sha(p)
    return out


def members(path):
    with zipfile.ZipFile(path) as z:
        res = {}
        for i in z.infolist():
            data = z.read(i.filename)
            res[i.filename] = {
                "sha256": hashlib.sha256(data).hexdigest(),
                "mode": oct(i.external_attr >> 16),
                "date_time": i.date_time,
                "compress_type": i.compress_type,
                "create_system": i.create_system,
                "extra": i.extra.hex(),
                "compress_size": i.compress_size,
            }
        order = [i.filename for i in z.infolist()]
    return res, order


def main():
    a, b = sys.argv[1], sys.argv[2]
    fa, fb = walk(a), walk(b)
    names = sorted(set(fa) | set(fb))
    same = diff = 0
    for n in names:
        if n not in fa:
            print(f"ONLY-IN-CANDIDATE {n}")
            diff += 1
        elif n not in fb:
            print(f"ONLY-IN-FROZEN    {n}")
            diff += 1
        elif fa[n] != fb[n]:
            print(f"DIFF              {n}  frozen={fa[n][:16]} candidate={fb[n][:16]}")
            diff += 1
        else:
            same += 1
    print(f"files: {same} identical, {diff} differ, {len(names)} total")
    for n in names:
        if not n.endswith(".zip") or n not in fa or n not in fb:
            continue
        ma, oa = members(os.path.join(a, n))
        mb, ob = members(os.path.join(b, n))
        bad = []
        if oa != ob:
            bad.append("member order differs")
        for m in sorted(set(ma) | set(mb)):
            if ma.get(m) != mb.get(m):
                keys = sorted(k for k in set(ma.get(m, {})) | set(mb.get(m, {}))
                              if ma.get(m, {}).get(k) != mb.get(m, {}).get(k))
                bad.append(f"{m}: {','.join(keys)}")
        print(f"zip {n}: " + ("members identical" if not bad else "; ".join(bad)))
    for n in names:
        if n.endswith("BUNDLE.json") and n in fa and n in fb and fa[n] != fb[n]:
            ja = json.load(open(os.path.join(a, n), encoding="utf-8"))
            jb = json.load(open(os.path.join(b, n), encoding="utf-8"))
            fields = [k for k in ja if ja[k] != jb.get(k)]
            print(f"BUNDLE.json {n}: differing top-level fields {fields}: "
                  + "; ".join(f"{k}: {ja[k]!r} -> {jb.get(k)!r}" for k in fields if k != "files"))


if __name__ == "__main__":
    main()
