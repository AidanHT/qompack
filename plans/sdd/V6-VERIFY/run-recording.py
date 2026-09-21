import datetime, hashlib, json, os, pathlib, subprocess, sys, time
root = pathlib.Path(__file__).resolve().parents[3]
run_id, *argv = sys.argv[1:]
logdir = root / "plans/sdd/V6-VERIFY/runs"
logdir.mkdir(parents=True, exist_ok=True)
log = logdir / (run_id + ".log")
meta = logdir / (run_id + ".json")
if log.exists() or meta.exists():
    raise SystemExit("Refusing to overwrite prior evidence: " + run_id)
def git(*args):
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()
record = {"id": run_id, "cwd": str(root), "argv": argv, "source_head": git("rev-parse","HEAD"),
          "source_dirty": git("status","--porcelain=v1"), "started_at": datetime.datetime.now().astimezone().isoformat(),
          "environment": {k:v for k,v in os.environ.items() if k in {"GOMAXPROCS","GOFLAGS","CGO_ENABLED"} or k.startswith("QOMPACK_")},
          "status":"running", "log": log.name}
meta.write_text(json.dumps(record, indent=2)+"\n", encoding="utf-8")
start=time.monotonic()
with log.open("w", encoding="utf-8") as stream:
    proc=subprocess.Popen(argv, cwd=root, stdout=stream, stderr=subprocess.STDOUT)
    code=proc.wait()
record.update(status="passed" if code==0 else "failed", exit_code=code,
              elapsed_seconds=round(time.monotonic()-start,3),
              finished_at=datetime.datetime.now().astimezone().isoformat(),
              log_sha256=hashlib.sha256(log.read_bytes()).hexdigest())
meta.write_text(json.dumps(record, indent=2)+"\n", encoding="utf-8")
print(json.dumps({k:record[k] for k in ("id","status","exit_code","elapsed_seconds","log")}), flush=True)
print("\n".join(log.read_text(encoding="utf-8", errors="replace").splitlines()[-18:]), flush=True)
sys.exit(code)
