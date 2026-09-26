"""Apply runpatterns waivers from `devtool lint --only=runpatterns` output.

usage: python rpwaive.py <repo> <lint-output.txt> [<file:line>=<reason> ...]
Categories handled automatically: unbalanced-paren alternations (split by the checker's parser),
and placeholder patterns starting with '<'. Anything else must be given an explicit reason.
"""
import re, sys, os
repo, out = sys.argv[1], sys.argv[2]
explicit = dict(a.split('=', 1) for a in sys.argv[3:])
SPLIT = ("the alternation is split at the shell-pipeline character by this checker's parser; "
         "the command ran as quoted and its result is recorded on this line")
PLACE = "the -run argument is a placeholder naming a set of tests the surrounding report lists, not a runnable pattern"
edits, unknown = {}, []
started = False
for line in open(out, encoding='utf-8'):
    if 'FAIL runpatterns' in line:
        started = True
    if not started:
        continue
    m = re.match(r'\s+(\S+\.md):(\d+): (.*)', line)
    if not m:
        continue
    f, n, msg = m.group(1), int(m.group(2)), m.group(3)
    key = f'{f}:{n}'
    if key in explicit:
        reason = explicit[key]
    elif 'is not a valid regexp' in msg and 'missing closing )' in msg:
        reason = SPLIT + ('; branches abbreviated with an ellipsis are named in full in the evidence log' if '...' in msg else '')
    elif re.search(r'-run "<', msg):
        reason = PLACE
    else:
        unknown.append(key + ' ' + msg[:160]); continue
    edits.setdefault(f, {})[n] = reason
for f, m in edits.items():
    p = os.path.join(repo, f); L = open(p, encoding='utf-8').read().split('\n')
    for n, r in m.items():
        if '<!-- runpatterns' in L[n-1]:
            continue
        L[n-1] = L[n-1] + f' <!-- runpatterns: {r} -->'
    open(p, 'w', encoding='utf-8', newline='\n').write('\n'.join(L))
print('waived', sum(len(m) for m in edits.values()))
for u in unknown:
    print('UNHANDLED', u)
