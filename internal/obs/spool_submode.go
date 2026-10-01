package obs

// Spool submode, worded for the user (V6 close-out D53(c), after D45 and D41's precedent).
//
// When hook deliveries stay over B-A for runtime.hotPath.breachWindows sample windows in a row, the
// daemon switches the hot path to spool submode (internal/daemon applyHotPathTransition): the hooks
// stop waiting for it and hand their captures to the spool, which the daemon replays. On a disk
// whose durable writes are slower than B-B's budget — WSL2, containers, network or encrypted
// filesystems — that is the designed behaviour of every long session, and the session loses
// nothing, so no surface may make it read as broken: the transition's log lines, `qompack status`
// and `qompack doctor` all say what happened, that nothing is lost, what ends it and how to tune it,
// in these words. The configuration keys are named exactly as docs/config-reference.md names them
// (TestSpoolSubmodeWording_NamesRealConfigKeys).

// SpoolSubmodeWhat says what happened and that nothing is lost.
const SpoolSubmodeWhat = "hook deliveries stayed over runtime.hotPath.budgetMs, usually because durable " +
	"writes on this disk take longer than runtime.budgets.l0IngestMs (WSL2, containers, network or " +
	"encrypted filesystems) and otherwise because the machine is heavily loaded, so hooks now hand " +
	"their captures to the spool and the daemon replays them; nothing is lost"

// SpoolSubmodeUntil says what ends it.
const SpoolSubmodeUntil = "a new session in this project, or the daemon's idle exit (there is no stop command)"

// SpoolSubmodeTune names the settings that move the threshold.
const SpoolSubmodeTune = "runtime.hotPath.budgetMs, runtime.budgets.l0IngestMs and runtime.daemon.ackDeadlineMs " +
	"(docs/troubleshooting.md, slow disks)"

// SpoolSubmodeKeys are the configuration keys the wording above names, for the test that ties them to
// the configuration's own key set.
var SpoolSubmodeKeys = []string{"runtime.hotPath.budgetMs", "runtime.budgets.l0IngestMs", "runtime.daemon.ackDeadlineMs"}
