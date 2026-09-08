package state

import (
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// Set is an ordered, append-only collection of derived-state records and the authority rules that
// govern transitions between them. It is a pure in-memory model: no I/O, no clock, no locking.
// A caller that shares one across goroutines supplies its own synchronization; a caller that
// wants durability uses [Set.Encode] and [Decode].
//
// "Append-only" is about identity and history, not about links: a record's id is written once and
// its content is never rewritten, while [Lineage] grows as transitions are applied. Nothing here
// removes a record. A superseded, corrected or conflicted record remains readable through
// [Set.Get] and [Set.All] forever — which is what makes "what did we believe before the user
// corrected us, and why" an answerable question.
type Set struct {
	byID  map[RecordID]*Record
	order []RecordID
}

// NewSet returns an empty Set.
func NewSet() *Set { return &Set{byID: map[RecordID]*Record{}} }

// Len reports how many records the set holds, superseded ones included.
func (s *Set) Len() int { return len(s.order) }

// Add admits a producer's record. The record must pass [Record.Validate], and its id must be
// unused: a second Add of the same id wraps [core.ErrAppendOnly] rather than updating anything,
// because an update is exactly how a low-authority derivation would overwrite a high-authority
// one without anybody being able to see that it happened.
//
// The stored record is a deep copy, so a caller mutating its slices afterwards cannot rewrite
// which observations the record cites.
func (s *Set) Add(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return s.admit(r)
}

// admit stores an already-checked record. It is the one place a record enters the set.
func (s *Set) admit(r Record) error {
	if _, dup := s.byID[r.ID]; dup {
		return fmt.Errorf("%w: state record %q already exists and is never rewritten", core.ErrAppendOnly, r.ID)
	}
	stored := r.Clone()
	s.byID[r.ID] = &stored
	s.order = append(s.order, r.ID)
	return nil
}

// Get returns a copy of the record with this id. The copy does not alias the set, so editing it
// changes nothing: authority, claim and evidence citations are fixed at admission.
func (s *Set) Get(id RecordID) (Record, bool) {
	r, ok := s.byID[id]
	if !ok {
		return Record{}, false
	}
	return r.Clone(), true
}

// All returns every record in admission order, superseded and conflicted ones included. It is the
// history, not the answer; [Set.Applicable] is the answer.
func (s *Set) All() []Record {
	out := make([]Record, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.byID[id].Clone())
	}
	return out
}

// Applicable returns the records that currently apply to q, in admission order.
//
// A record is withheld when it is superseded, when it is party to an unresolved conflict, when
// its authority label is not one this version can classify, when it cites no evidence, or when
// its scope does not reach q. Conflict records themselves are never returned here: a conflict is
// a question, and [Set.Conflicts] is where a caller asks for it.
func (s *Set) Applicable(q Scope) []Record {
	out := []Record{}
	for _, id := range s.order {
		r := s.byID[id]
		if s.applies(*r, q) {
			out = append(out, r.Clone())
		}
	}
	return out
}

func (s *Set) applies(r Record, q Scope) bool {
	if r.Authority == core.AuthorityConflict || !r.Authority.Valid() {
		return false
	}
	if len(r.Sources) == 0 || r.Superseded() {
		return false
	}
	for _, cid := range r.Lineage.ConflictedBy {
		if c, ok := s.byID[cid]; ok && !c.Conflict.Resolved() {
			return false
		}
	}
	return r.Scope.AppliesTo(q)
}

// Conflicts returns the unresolved conflicts in scope for q, in admission order. A caller that
// renders state is expected to render these too: an unresolved conflict is a real answer — "two
// sources disagree and nobody authorized has said which is right" — and dropping it would turn a
// known disagreement into a silent choice.
func (s *Set) Conflicts(q Scope) []Record {
	out := []Record{}
	for _, id := range s.order {
		r := s.byID[id]
		if r.Authority == core.AuthorityConflict && !r.Conflict.Resolved() && r.Scope.AppliesTo(q) {
			out = append(out, r.Clone())
		}
	}
	return out
}

// Supersede admits newer and records it as the successor of target, linking both directions and
// retaining the history on both sides. Nothing is deleted: target stays readable, keeps its own
// authority, and keeps citing exactly the observations it always cited.
//
// It is refused, with nothing changed, when target does not exist ([core.ErrNotFound]), when
// newer would not pass [Set.Add], when [CanSupersede] says newer's authority may not take
// target's place, when the two records are not in the same scope, or when target has already been
// superseded — a chain is extended at its head, not in the middle.
func (s *Set) Supersede(newer Record, target RecordID, at core.TurnIndex, note string) error {
	return s.supersede(newer, target, at, note, false)
}

// Correct is [Set.Supersede] by an authorized correcting source: it additionally records the
// correction lineage in both directions, so "this was corrected by the user" and "this was
// replaced by a better observation" stay distinguishable forever.
//
// Only [core.AuthorityUserCorrection] and [core.AuthorityExplicitDecision] may correct. This is
// the "a later authorized correction supersedes an obsolete instruction" path, and it is the only
// path by which an authoritative record loses its standing.
func (s *Set) Correct(newer Record, target RecordID, at core.TurnIndex, note string) error {
	return s.supersede(newer, target, at, note, true)
}

func (s *Set) supersede(newer Record, target RecordID, at core.TurnIndex, note string, correcting bool) error {
	existing, ok := s.byID[target]
	if !ok {
		return fmt.Errorf("%w: state record %q", core.ErrNotFound, target)
	}
	if err := newer.Validate(); err != nil {
		return err
	}
	if _, dup := s.byID[newer.ID]; dup {
		return fmt.Errorf("%w: state record %q already exists and is never rewritten", core.ErrAppendOnly, newer.ID)
	}
	if correcting && !Authoritative(newer.Authority) {
		return fmt.Errorf("%w: %s record %q may not correct %q; only a user correction or an explicit decision may",
			core.ErrContract, newer.Authority, newer.ID, target)
	}
	if !CanSupersede(newer.Authority, existing.Authority) {
		return fmt.Errorf("%w: %s record %q may not supersede %s record %q",
			core.ErrContract, newer.Authority, newer.ID, existing.Authority, target)
	}
	if newer.Scope != existing.Scope {
		return fmt.Errorf("%w: state record %q is scoped elsewhere than %q; a supersession stays inside one scope",
			core.ErrContract, newer.ID, target)
	}
	if existing.Superseded() {
		return fmt.Errorf("%w: state record %q was already superseded by %q; supersede the head of the chain",
			core.ErrContract, target, existing.Lineage.SupersededBy[0])
	}

	if err := s.admit(newer); err != nil {
		return err
	}
	added := s.byID[newer.ID]

	existing.Lineage.SupersededBy = append(existing.Lineage.SupersededBy, added.ID)
	added.Lineage.Supersedes = append(added.Lineage.Supersedes, target)
	entry := LineageEntry{
		Kind: LineageSuperseded, Authority: added.Authority, Turn: at, Note: note,
	}
	existing.note(entry, added.ID)
	added.note(entry, target)

	if correcting {
		existing.Lineage.CorrectedBy = append(existing.Lineage.CorrectedBy, added.ID)
		added.Lineage.Corrects = append(added.Lineage.Corrects, target)
		entry.Kind = LineageCorrected
		existing.note(entry, added.ID)
		added.note(entry, target)
	}
	return nil
}

// Conflict admits marker as the representation of a disagreement between parties. The marker must
// carry [core.AuthorityConflict] — which is why it cannot be admitted through [Set.Add] — and
// must otherwise be a complete record: a conflict cites the evidence it arose from like anything
// else here.
//
// Every party is withheld from [Set.Applicable] from now until an authorized source resolves the
// conflict. No party is preferred, ranked, merged or averaged, and none is removed.
func (s *Set) Conflict(marker Record, parties []RecordID, reason string, at core.TurnIndex) error {
	if marker.Authority != core.AuthorityConflict {
		return fmt.Errorf("%w: a conflict record must carry %q authority, not %q",
			core.ErrContract, core.AuthorityConflict, marker.Authority)
	}
	if err := marker.validateFields(); err != nil {
		return err
	}
	if len(parties) < 2 {
		return fmt.Errorf("%w: a conflict needs at least two parties, got %d", core.ErrContract, len(parties))
	}
	seen := map[RecordID]bool{}
	for _, id := range parties {
		party, ok := s.byID[id]
		if !ok {
			return fmt.Errorf("%w: conflict party %q", core.ErrNotFound, id)
		}
		if seen[id] {
			return fmt.Errorf("%w: conflict party %q is listed twice", core.ErrContract, id)
		}
		seen[id] = true
		if party.Scope != marker.Scope {
			return fmt.Errorf("%w: conflict party %q is scoped elsewhere than the conflict", core.ErrContract, id)
		}
		if party.Superseded() {
			return fmt.Errorf("%w: conflict party %q is already superseded", core.ErrContract, id)
		}
	}

	marker.Conflict = &Conflict{Parties: cloneIDs(parties), Reason: reason}
	if err := s.admit(marker); err != nil {
		return err
	}
	added := s.byID[marker.ID]
	for _, id := range parties {
		party := s.byID[id]
		party.Lineage.ConflictedBy = append(party.Lineage.ConflictedBy, added.ID)
		party.note(LineageEntry{
			Kind: LineageConflicted, Authority: core.AuthorityConflict, Turn: at, Note: reason,
		}, added.ID)
	}
	return nil
}

// Resolve ends the conflict named by conflictID in favour of the record named by by, which must
// already be in the set and must be authoritative: a hypothesis, an extraction or a tool
// observation cannot decide a disagreement it is part of, however plausible it looks.
//
// The resolution is recorded on the conflict, the losing parties are superseded by the resolving
// record, and every side keeps its history. A resolved conflict is not re-resolvable: a second
// call wraps [core.ErrContract] rather than quietly re-deciding.
func (s *Set) Resolve(conflictID, by RecordID, at core.TurnIndex, reason string) error {
	conflict, ok := s.byID[conflictID]
	if !ok {
		return fmt.Errorf("%w: state record %q", core.ErrNotFound, conflictID)
	}
	if conflict.Authority != core.AuthorityConflict || conflict.Conflict == nil {
		return fmt.Errorf("%w: state record %q is not a conflict", core.ErrContract, conflictID)
	}
	if conflict.Conflict.Resolved() {
		return fmt.Errorf("%w: conflict %q was already resolved by %q",
			core.ErrContract, conflictID, conflict.Conflict.Resolution.By)
	}
	winner, ok := s.byID[by]
	if !ok {
		return fmt.Errorf("%w: resolving record %q", core.ErrNotFound, by)
	}
	if !Authoritative(winner.Authority) {
		return fmt.Errorf("%w: %s record %q may not resolve conflict %q; only a user correction or an explicit decision may",
			core.ErrContract, winner.Authority, by, conflictID)
	}
	if winner.Scope != conflict.Scope {
		return fmt.Errorf("%w: resolving record %q is scoped elsewhere than conflict %q",
			core.ErrContract, by, conflictID)
	}

	conflict.Conflict.Resolution = &Resolution{
		By: by, Authority: winner.Authority, Turn: at, Reason: reason,
	}
	resolved := LineageEntry{
		Kind: LineageResolved, Authority: winner.Authority, Turn: at, Note: reason,
	}
	conflict.note(resolved, by)
	winner.note(resolved, conflictID)

	for _, id := range conflict.Conflict.Parties {
		if id == by {
			continue
		}
		party := s.byID[id]
		if party.Superseded() {
			continue
		}
		party.Lineage.SupersededBy = append(party.Lineage.SupersededBy, by)
		winner.Lineage.Supersedes = append(winner.Lineage.Supersedes, id)
		entry := LineageEntry{
			Kind: LineageSuperseded, Authority: winner.Authority, Turn: at, Note: reason,
		}
		party.note(entry, by)
		winner.note(entry, id)
	}
	return nil
}

// note appends one history entry naming the record on the far side of the transition. History is
// additive only; nothing in this package shortens it.
func (r *Record) note(e LineageEntry, other RecordID) {
	e.Other = other
	r.Lineage.History = append(r.Lineage.History, e)
}
