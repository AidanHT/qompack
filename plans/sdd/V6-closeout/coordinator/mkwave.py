"""mkwave.py <template.js> <tasks.js> <out.js> <from-tag> <to-tag> <base> <name> <description> <detail> <others>

Builds a wave script from an earlier wave's script: keeps COMMON, the schemas and the pipeline, swaps
the worktree/branch tag (e.g. w7b -> w8), the base commit, the meta block and the WS list.
"""
import re
import sys

tpl, tasks_p, out, frm, to, base, name, desc, detail, others = sys.argv[1:11]
w = open(tpl, encoding="utf-8").read()
tasks = open(tasks_p, encoding="utf-8").read()
head = w[: w.index("const WS = []")]
tail = w[w.index("const RESULT = ") :]
head = re.sub(r"name: '[^']*'", f"name: '{name}'", head, count=1)
head = re.sub(r"description: '[^']*'", "description: " + repr(desc), head, count=1)
head = re.sub(r"\{ title: 'Implement', detail: '[^']*' \}", "{ title: 'Implement', detail: " + repr(detail) + " }", head, count=1)
head = re.sub(r"const BASE = '[0-9a-f]+'", f"const BASE = '{base}'", head, count=1)
head = re.sub(r"You are one of [^.]*? parallel workstreams( \([^)]*\))?", f"You are one of the parallel workstreams ({others})", head, count=1)
head = head.replace(f"qompack-cx-{frm}-", f"qompack-cx-{to}-").replace(f"closeout/{frm}-", f"closeout/{to}-")
head = head.replace(f"cx-{frm}-", f"cx-{to}-").replace(f"{frm}-${{w.ws}}", f"{to}-${{w.ws}}")
head = head.replace(f"/scratchpad/{frm}'", f"/scratchpad/{to}'")
tail = tail.replace(f"qompack-cx-{frm}-", f"qompack-cx-{to}-").replace(f"closeout/{frm}-", f"closeout/{to}-")
tail = re.sub(r"wave-[0-9a-z]+ workstream", f"wave-{to[1:]} workstream", tail)
open(out, "w", encoding="utf-8", newline="\n").write(head + tasks + "\n" + tail)
print(out)
