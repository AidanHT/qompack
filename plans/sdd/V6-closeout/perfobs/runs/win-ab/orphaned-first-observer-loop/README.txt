Written by the FIRST observer A/B loop after I stopped its parent task at ~22:54Z: TaskStop ended the
outer shell but the ab_win.sh child survived (the background-kill-orphans trap) and kept running,
re-creating these files and co-loading every later A/B until I found and stopped it at ~00:00Z.
Its "new" samples used whichever bin/new binary existed at the time (92ad1db until 22:53Z, de83e44
after). Not used in any table. The exit=127 lines at 23:51:55Z occurred in two loops at once and were
not investigated further.
