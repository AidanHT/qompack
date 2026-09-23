#!/bin/sh
# Linux correctness pass for closeout/perfobs: each touched package in full, as the non-root
# user qtest (root makes permission fixtures vacuous), GOMAXPROCS=4.
SUF=$1
W=/work/cx-perfobs-$SUF
A=$W-artifacts
mkdir -p $A /tmp/cx-perfobs-$SUF-home
chown qtest:qtest /tmp/cx-perfobs-$SUF-home
chmod 777 $A
for p in canon sketch negknow checkpoint observer; do
  cd $W-new/internal/$p || exit 2
  su qtest -s /bin/sh -c "HOME=/tmp/cx-perfobs-$SUF-home GOMAXPROCS=4 $W-bin/new/$p.test -test.count=1 -test.timeout=30m" > $A/linux-$p-full.txt 2>&1
  echo "exit=$? user=$(id -un qtest) pkg=$p commit=$(git -C $W-new rev-parse --short HEAD)" >> $A/linux-$p-full.txt
  tail -2 $A/linux-$p-full.txt
done
echo linux-tests-done > $A/linux-tests-done.txt
