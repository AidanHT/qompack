#!/bin/sh
# recrun.sh <repo> <evidence-dir> <run-id> -- <command...>
# Records a run the V6 way: <id>.json (argv, cwd, source head/dirty, env, start/end, exit, log sha256)
# and <id>.log (combined output). Refuses to overwrite an existing record.
set -u
repo=$1; ev=$2; id=$3; shift 3
[ "$1" = "--" ] && shift
mkdir -p "$ev"
json="$ev/$id.json"; log="$ev/$id.log"
if [ -e "$json" ] || [ -e "$log" ]; then echo "refusing to overwrite $id" >&2; exit 2; fi
head=$(git -C "$repo" rev-parse HEAD)
dirty=$(git -C "$repo" status --porcelain | tr '\n' ';')
start=$(date -Iseconds)
( cd "$repo" && "$@" ) > "$log" 2>&1
rc=$?
end=$(date -Iseconds)
sha=$(sha256sum "$log" | cut -d' ' -f1)
python - "$json" "$id" "$repo" "$head" "$dirty" "$start" "$end" "$rc" "$sha" "$@" <<'EOF'
import json, os, sys
p, rid, repo, head, dirty, start, end, rc, sha = sys.argv[1:10]
argv = sys.argv[10:]
env = {k: v for k, v in os.environ.items() if k.startswith(("QOMPACK_", "GO", "CGO_"))}
json.dump({"id": rid, "cwd": repo, "argv": argv, "source_head": head, "source_dirty": dirty,
           "environment": env, "started_at": start, "ended_at": end, "exit_code": int(rc),
           "log": os.path.basename(p)[:-5] + ".log", "log_sha256": sha},
          open(p, "w", encoding="utf-8", newline="\n"), indent=2)
EOF
echo "run $id exit=$rc"
exit $rc
