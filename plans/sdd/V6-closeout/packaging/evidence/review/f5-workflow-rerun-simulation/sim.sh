#!/usr/bin/env bash
# Simulates marketplace.yml's pull-request step against a local bare "origin", twice per variant:
# a first run, then a re-run of the job from a fresh checkout (the state a partial failure leaves).
set -u
export SIMROOT="$PWD" PATH="$PWD:$PATH" TAG=v9.9.9 GIT_AUTHOR_NAME=sim GIT_AUTHOR_EMAIL=sim@invalid GIT_COMMITTER_NAME=sim GIT_COMMITTER_EMAIL=sim@invalid
rm -rf origin.git seed run-* prs
git init -q --bare origin.git
git init -q -b develop seed && (cd seed && printf '/dist/\n' > .gitignore && echo readme > README && git add . && git commit -qm "develop base" && git tag v9.9.9 && git remote add origin ../origin.git && git push -q origin develop v9.9.9)
run() { # variant n
  local v=$1 n=$2 d="run-$1-$2"
  git clone -q origin.git "$d" && cd "$d" || return 99
  if [ "$v" = new ]; then git -c advice.detachedHead=false checkout -q "refs/tags/$TAG"; else git checkout -q develop; fi
  mkdir -p dist/generated && echo '{"name":"qompack"}' > dist/generated/marketplace.json
  [ "$v" = old ] && { mkdir -p .claude-plugin; cp dist/generated/marketplace.json .claude-plugin/marketplace.json; }
  bash -e "../step-$v.sh" > ../"$d.log" 2>&1; local rc=$?
  cd ..; echo "variant=$v run=$n exit=$rc"; sed 's/^/    /' "$d.log"
  sleep 1 # a later run commits with a later timestamp, so its commit differs, as on a real re-run
}
run old 1; run old 2
rm -rf prs; git -C origin.git update-ref -d refs/heads/marketplace/$TAG
run new 1; run new 2
echo "origin branches after the new variant:"; git -C origin.git for-each-ref --format='    %(refname) %(objectname:short)' refs/heads
