#!/bin/sh
# usage: linux-test.sh <workdir> <label> <pkg> <run-regex>   (runs as qtest, non-root)
W=$1; L=$2; PKG=$3; RX=$4
mkdir -p "$W/out"
chown -R qtest "$W"
su qtest -s /bin/sh -c "cd '$W' && export PATH=/usr/local/go/bin:\$PATH GOMODCACHE=/go/pkg/mod GOFLAGS=-mod=readonly GOTOOLCHAIN=local GOMAXPROCS=4 && id && go test $PKG -run '$RX' -count=1 -timeout=30m -v > out/$L.txt 2>&1; echo exit=\$? >> out/$L.txt"
tail -3 "$W/out/$L.txt"
