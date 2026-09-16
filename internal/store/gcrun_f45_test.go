package store

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
)

// loudCountingLogger counts Loud calls; every other Logger method is a no-op.
type loudCountingLogger struct {
	mu sync.Mutex
	n  int
}

func (l *loudCountingLogger) With(...any) logging.Logger { return l }
func (l *loudCountingLogger) Debug(string, ...any)       {}
func (l *loudCountingLogger) Info(string, ...any)        {}
func (l *loudCountingLogger) Warn(string, ...any)        {}
func (l *loudCountingLogger) Error(string, ...any)       {}

func (l *loudCountingLogger) Loud(string, ...any) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *loudCountingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func withLog(l logging.Logger) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Log = l }
}

// TestDeclaredRetentionLines_CountsEachBadLineAndLoudsOnce is finding F4-5's owning-package pin
// plus finding 10: two unreadable retention-roots lines produce exactly one Loud and increment
// store.retention_roots_badline once per line.
func TestDeclaredRetentionLines_CountsEachBadLineAndLoudsOnce(t *testing.T) {
	log := &loudCountingLogger{}
	tp := newTestStore(t, withLog(log))
	fn := tp.Store.declaredRetentionLines()

	class, _, ok := fn([]byte(`{"hash":"not-a-hash"}`))
	require.True(t, ok, "an unreadable line is still retained")
	require.Equal(t, RetentionRollback, class)

	class, _, ok = fn([]byte(`not-json-at-all`))
	require.True(t, ok)
	require.Equal(t, RetentionRollback, class)

	require.Equal(t, 1, log.count(), "Loud once per pass, not once per bad line")
	require.Equal(t, int64(2), tp.counter("store.retention_roots_badline"),
		"the counter counts each bad line")
}
