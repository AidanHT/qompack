import json, sys
src, out = sys.argv[1], sys.argv[2]
lines = []
for raw in open(src, encoding='utf-8'):
    try: e = json.loads(raw)
    except Exception: continue
    if e.get('type') != 'assistant': continue
    ts = e.get('timestamp', '')[:19]
    for b in (e.get('message') or {}).get('content') or []:
        if b.get('type') == 'text' and b.get('text', '').strip():
            lines.append(f"[{ts}] TEXT: {b['text'].strip()}")
        elif b.get('type') == 'tool_use':
            inp = b.get('input') or {}
            s = inp.get('command') or inp.get('file_path') or inp.get('pattern') or json.dumps(inp)[:200]
            lines.append(f"[{ts}] {b.get('name')}: {str(s)[:400]}")
open(out, 'w', encoding='utf-8').write('\n'.join(lines))
print(out, len(lines))
