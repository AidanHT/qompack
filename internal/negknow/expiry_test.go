package negknow_test

import (
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/stretchr/testify/require"
)

// The three fixed instants every lifetime test reads against. They are plain constants rather
// than a clock because Disposition takes now as an argument and has no clock of its own — which
// is what makes an applicability transcript reproducible from its inputs.
const (
	beforeExpiry = core.UnixMilli(1_000)
	atExpiry     = core.UnixMilli(2_000)
	afterExpiry  = core.UnixMilli(3_000)
)

// TestLifecycleEvent_Valid_ClosedSet pins that an event value this build does not know is refused
// rather than read as LifecycleNone.
func TestLifecycleEvent_Valid_ClosedSet(t *testing.T) {
	t.Parallel()

	for _, e := range []negknow.LifecycleEvent{
		negknow.LifecycleNone, negknow.LifecycleDeleted,
		negknow.LifecycleSuperseded, negknow.LifecycleCorrected,
	} {
		require.True(t, e.Valid(), string(e))
	}
	require.False(t, negknow.LifecycleEvent("archived").Valid())
	require.False(t, negknow.LifecycleEvent("Deleted").Valid(), "spelling is not normalized")
}

// TestLifecycle_Disposition_Table is the precedence specification.
//
// The correction rows are the ones worth reading twice: a corrected record whose expiry has ALSO
// passed reports DispositionCorrected, not DispositionExpired, because "a user said this was
// wrong" is a stronger statement than "this got old" and a transcript that said only the latter
// would understate why the claim must not come back.
func TestLifecycle_Disposition_Table(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		life negknow.Lifecycle
		now  core.UnixMilli
		want negknow.Disposition
	}{
		{
			name: "the zero lifecycle is live",
			now:  afterExpiry,
			want: negknow.DispositionLive,
		},
		{
			name: "a declared expiry still in the future is live",
			life: negknow.Lifecycle{ExpiresAt: atExpiry},
			now:  beforeExpiry,
			want: negknow.DispositionLive,
		},
		{
			name: "expiry is inclusive: the timestamp itself is already expired",
			life: negknow.Lifecycle{ExpiresAt: atExpiry},
			now:  atExpiry,
			want: negknow.DispositionExpired,
		},
		{
			name: "a declared expiry in the past",
			life: negknow.Lifecycle{ExpiresAt: atExpiry},
			now:  afterExpiry,
			want: negknow.DispositionExpired,
		},
		{
			name: "deletion outranks a live expiry",
			life: negknow.Lifecycle{Event: negknow.LifecycleDeleted, ExpiresAt: atExpiry},
			now:  beforeExpiry,
			want: negknow.DispositionDeleted,
		},
		{
			name: "supersession outranks a live expiry",
			life: negknow.Lifecycle{Event: negknow.LifecycleSuperseded, By: "elim_ffff0000aaaa"},
			now:  beforeExpiry,
			want: negknow.DispositionSuperseded,
		},
		{
			name: "correction outranks a passed expiry",
			life: negknow.Lifecycle{Event: negknow.LifecycleCorrected, ExpiresAt: atExpiry},
			now:  afterExpiry,
			want: negknow.DispositionCorrected,
		},
		{
			name: "an unrecognized event is uncertain, never live",
			life: negknow.Lifecycle{Event: "archived"},
			now:  beforeExpiry,
			want: negknow.DispositionUncertain,
		},
		{
			name: "an unknown event is uncertain even with a live expiry",
			life: negknow.Lifecycle{Event: "archived", ExpiresAt: atExpiry},
			now:  beforeExpiry,
			want: negknow.DispositionUncertain,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.life.Disposition(tc.now))
		})
	}
}

// TestDisposition_LiveIsTheOnlyPermissiveValue pins that the zero Disposition does not read as
// permission — the property that makes forgetting to set one safe.
func TestDisposition_LiveIsTheOnlyPermissiveValue(t *testing.T) {
	t.Parallel()

	require.False(t, negknow.Disposition(0).Live(), "the zero disposition is uncertain, not live")
	require.True(t, negknow.DispositionLive.Live())
	for _, d := range []negknow.Disposition{
		negknow.DispositionUncertain, negknow.DispositionExpired, negknow.DispositionDeleted,
		negknow.DispositionSuperseded, negknow.DispositionCorrected,
	} {
		require.False(t, d.Live(), d.String())
	}
}

// TestDisposition_String_Table pins the transcript spellings and the fallback.
func TestDisposition_String_Table(t *testing.T) {
	t.Parallel()

	for d, want := range map[negknow.Disposition]string{
		negknow.DispositionUncertain:  "uncertain",
		negknow.DispositionLive:       "live",
		negknow.DispositionExpired:    "expired",
		negknow.DispositionDeleted:    "deleted",
		negknow.DispositionSuperseded: "superseded",
		negknow.DispositionCorrected:  "corrected",
		negknow.Disposition(200):      "uncertain",
	} {
		require.Equal(t, want, d.String())
	}
}

// TestLifecycle_ExpiryDeclared pins that "no shelf life was declared" and "never expires" are the
// same field value and a different question from whether the evidence has ended.
func TestLifecycle_ExpiryDeclared(t *testing.T) {
	t.Parallel()

	require.False(t, negknow.Lifecycle{}.ExpiryDeclared())
	require.True(t, negknow.Lifecycle{ExpiresAt: atExpiry}.ExpiryDeclared())

	// Undeclared expiry is still live: it is a gap Applies acts on, not an end in itself.
	require.Equal(t, negknow.DispositionLive, negknow.Lifecycle{}.Disposition(afterExpiry))
}

// TestDisposition_OmissionsNameTheCauseAndNeverSayReuseAnyway pins the transcript half: every
// non-live disposition produces a reason and a recovery, the recovery is re-verification rather
// than "reuse it anyway", and a recorded cause is named.
func TestDisposition_OmissionsNameTheCauseAndNeverSayReuseAnyway(t *testing.T) {
	t.Parallel()

	// Reached through Applies, which is the only exported path to the omission renderer.
	for _, tc := range []struct {
		name       string
		life       negknow.Lifecycle
		wantReason string
	}{
		{"expired", negknow.Lifecycle{ExpiresAt: atExpiry}, "evidence expired at 2000"},
		{
			"deleted",
			negknow.Lifecycle{Event: negknow.LifecycleDeleted, By: "retention"},
			"evidence was deleted by retention",
		},
		{
			"superseded",
			negknow.Lifecycle{Event: negknow.LifecycleSuperseded, By: "elim_abc123abc123"},
			"evidence was superseded by elim_abc123abc123",
		},
		{
			"corrected",
			negknow.Lifecycle{Event: negknow.LifecycleCorrected},
			"evidence was corrected",
		},
		{
			"unknown event",
			negknow.Lifecycle{Event: "archived"},
			`unrecognized lifecycle event "archived"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := negknow.Applies(negknow.ReuseRequest{
				From: scopeAt(treeMain, "develop", "v1", "sess-a"),
				To:   scopeAt(treeMain, "develop", "v1", "sess-a"),
				Life: tc.life,
				Now:  afterExpiry,
			})
			require.False(t, got.Allowed())
			require.Len(t, got.Omissions, 1)
			require.Equal(t, tc.wantReason, got.Omissions[0].Reason)
			require.NotEmpty(t, got.Omissions[0].Recovery, "every refusal names a way forward")
			require.NotContains(t, got.Omissions[0].Recovery, "anyway")
		})
	}
}

// TestApplies_LifecycleRefusalsEvenInsideOneSession pins that the lifetime gate runs BEFORE the
// relation gate: a corrected record does not come back just because it never left its session.
func TestApplies_LifecycleRefusalsEvenInsideOneSession(t *testing.T) {
	t.Parallel()

	same := scopeAt(treeMain, "develop", "v1", "sess-a")
	for _, tc := range []struct {
		name string
		life negknow.Lifecycle
		want negknow.Reusability
	}{
		{"corrected", negknow.Lifecycle{Event: negknow.LifecycleCorrected}, negknow.ReuseDenied},
		{"deleted", negknow.Lifecycle{Event: negknow.LifecycleDeleted}, negknow.ReuseDenied},
		{"superseded", negknow.Lifecycle{Event: negknow.LifecycleSuperseded}, negknow.ReuseDenied},
		{"expired", negknow.Lifecycle{ExpiresAt: atExpiry}, negknow.ReuseDenied},
		{"uninterpretable", negknow.Lifecycle{Event: "archived"}, negknow.ReuseWithheld},
		{"live", negknow.Lifecycle{}, negknow.ReuseAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := negknow.Applies(negknow.ReuseRequest{
				From: same, To: same, Life: tc.life, Now: afterExpiry,
				EvidenceAuthority: core.AuthorityToolObservation,
			})
			require.Equal(t, tc.want, got.Reuse)
		})
	}
}
