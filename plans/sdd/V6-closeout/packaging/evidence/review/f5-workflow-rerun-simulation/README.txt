Reviewer finding 5 (marketplace.yml re-run), local simulation, 2026-09-22, windows/amd64 Git Bash
(git 2.47.0.windows.2). Not a GitHub Actions run: no release exists, so marketplace.yml has never run.

step-old.sh / step-new.sh are the pull-request step's `run:` block extracted verbatim (awk, see the
command below) from d9b0069's marketplace.yml and from this fix's. sim.sh runs each twice against a
local bare "origin" - a first run, then a re-run from a fresh clone, which is the state a partial
failure leaves - with `gh` replaced by the stub in this directory (one pull request per head).

Result (sim-output.txt): old run 2 exits 1, "! [rejected] marketplace/v9.9.9 -> marketplace/v9.9.9
(non-fast-forward)". New run 2 exits 0: "+ 797bc0a...f518781 ... (forced update)" under the lease,
then "pull request #1 already proposes marketplace/v9.9.9; the push above updated it".

Generator step: `go run ./tools/devtool marketplace --tag v0.3.0-rc.pkg --checksums
../c7.5-marketplace/checksums-v0.3.0-rc.pkg.txt --out <scratch>/generated.json`, then --validate, then
cmp against ../c7.5-marketplace/generated-marketplace-v0.3.0-rc.pkg.json: cmp exit 0.

Extraction: awk -v name="<step name>" 'index($0, "- name: " name)>0 {f=1; next}
  f && /^        run: \|/ {r=1; next} r && /^      - / {exit} r {sub(/^          /, ""); print}'
