package negknow

import "github.com/qompack/qompack/internal/core"

// Inherited names one session whose session-scoped records another session's conversation holds:
// the records Session made at or before Until (coordinator decision D49 of the V6 close-out,
// finding F-C4-UAT06-1). A `--fork-session` continues its parent's conversation, so what the
// parent eliminated before the fork is the fork's negative knowledge too; what it eliminated after
// is not, and a sibling session that merely shares the project inherits nothing.
type Inherited struct {
	// Session is the ancestor session.
	Session core.SessionID
	// Until is the last record time the inheriting session's conversation holds.
	Until core.UnixMilli
}

// viewer is who a read is made for: the session, and the sessions whose session-scoped records
// that session's conversation holds (Deps.Ancestry), resolved once per read.
type viewer struct {
	sess core.SessionID
	anc  []Inherited
}

// owns reports whether a session-scoped record r is the viewer's own negative knowledge: made by
// its session, or by an ancestor at or before the moment the conversation left it.
func (v viewer) owns(r Record) bool {
	if r.Session == v.sess {
		return true
	}
	for _, a := range v.anc {
		if r.Session == a.Session && r.TS <= a.Until {
			return true
		}
	}
	return false
}

// viewerOf resolves session s's viewer through Deps.Ancestry. An empty session inherits nothing.
func (l *ledger) viewerOf(s core.SessionID) viewer {
	v := viewer{sess: s}
	if s != "" && l.deps.Ancestry != nil {
		v.anc = l.deps.Ancestry(s)
	}
	return v
}
