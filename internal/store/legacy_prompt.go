package store

import (
	"encoding/json"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Prompt captures a build before the V6 close-out's prompt link wrote (D3, cross-version).
//
// Those builds — the development builds up to 0.2.99-prev (301a8e9), the live lane's "previous
// build" — published every captured prompt: a prompt_<session>_<turn> tool_use record whose root is
// the prompt's bytes and whose args digest is the digest of {"prompt": <text>}, bound to no
// observation. What they never did was join the capture sidecar to that record (LinkCaptureReference
// ran for tool and subagent captures only), so every prompt sidecar they wrote says
// published:false. The current build links prompts, and its leased prompt records carry an args
// digest bound to the delivery's observation and an observation intent; but its UNLEASED records
// carry the same unbound digest an earlier build wrote, so the digest alone does not identify an
// earlier build's record. LegacyPromptRecords says which records may claim.
//
// A released build never wrote a sidecar at all: v0.2.0 has no records/captures/ tree, and a store
// it wrote carries nothing this file reads.
//
// Such a sidecar's reference EXISTS, so it is not a publication gap, and the accounting reads it as
// published: the sidecar's own payload names the prompt, its session names the record's session, and
// a record in that session carrying that digest, published before the session's first
// observation-bound prompt, is its reference. Each record accounts for one
// sidecar, so two deliveries of the same words against one record still leave one gap. Nothing is
// written, re-linked or re-minted: the sidecar stays exactly as the earlier build wrote it.

// promptRecordTool is the Tool a prompt's tool_use record carries (observer's userPromptSubmit).
const promptRecordTool = "UserPromptSubmit"

// promptRecordIDPrefix begins every prompt record id (observer.VerbatimPromptID: "prompt_<s>_<turn>").
const promptRecordIDPrefix = "prompt_"

// LegacyPromptKey identifies an earlier build's prompt record: its session and its unbound args
// digest.
type LegacyPromptKey struct {
	Session core.SessionID
	Digest  core.Hash
}

// IsPromptRecord reports whether a tool_use record is a captured prompt's (id and tool as observer's
// VerbatimPromptID and userPromptSubmit spell them).
func IsPromptRecord(id core.ToolUseID, tool string) bool {
	return tool == promptRecordTool && strings.HasPrefix(string(id), promptRecordIDPrefix)
}

// LegacyPromptDigest is the args digest an earlier build recorded for the prompt a UserPromptSubmit
// capture's payload carries: ArgsDigest over {"prompt": <text>}, exactly observer.promptArgs. ok is
// false for a payload that names no prompt.
func LegacyPromptDigest(payload []byte) (core.Hash, bool) {
	var p struct {
		Prompt *string `json:"prompt"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Prompt == nil {
		return core.Hash{}, false
	}
	args, err := json.Marshal(struct {
		Prompt string `json:"prompt"`
	}{*p.Prompt})
	if err != nil {
		return core.Hash{}, false
	}
	d, _ := ArgsDigest(args)
	return d, true
}

// LegacyPromptCounter is implemented by the store: the prompt records an earlier build's unlinked
// prompt sidecars may claim (ClaimLegacyPrompt), counted by (session, args digest). The publication
// audit claims from it, and so does `qompack fsck`'s captures row, through a read-only open, so the
// two rows cannot disagree.
type LegacyPromptCounter interface {
	LegacyPromptRecords() map[LegacyPromptKey]int
}

var _ LegacyPromptCounter = (*FSStore)(nil)

// LegacyPromptRecords counts the prompt records that may account for an earlier build's unlinked
// prompt sidecar, by (session, args digest).
//
// An unbound digest alone does not identify an earlier build's record: the current build writes the
// same {"prompt": <text>} digest for an UNLEASED delivery (observer's promptDeliveryDigest with no
// observation, as when the delivery journal is unavailable), and such a record must not account for
// a leased delivery of the same words that the current build left at stage 1 — that is a real gap.
// What the current build does that no build before the prompt link did is publish a leased prompt
// through an observation intent (index/observations.jsonl, which those builds never wrote). So a
// session's records from the turn of its first intent-bound prompt on are the current build's, and
// only its records BEFORE that turn — all of them, in a session no current build touched — may
// claim. Turns are publication order within a session, so an upgrade in the middle of a session
// splits it cleanly.
//
// When the observation intents cannot be read completely (the same condition the publication audit
// reports as "observation intent integrity is unavailable"), no record claims anything: every such
// sidecar stays a gap rather than being excused on partial evidence. The residual this cannot see is
// a session whose every prompt the current build delivered unleased and whose first leased prompt
// is itself the stage-1 gap; the durable record carries nothing that would tell it apart.
func (s *FSStore) LegacyPromptRecords() map[LegacyPromptKey]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[LegacyPromptKey]int{}
	if s.obsSidecarUncertain || len(s.obsAmbiguous) > 0 || len(s.obsUnavailable) > 0 {
		return out
	}
	firstBound := map[core.SessionID]core.TurnIndex{}
	for _, b := range s.obsBindings {
		r := b.intent.rec
		if !IsPromptRecord(r.ID, r.Tool) {
			continue
		}
		if t, seen := firstBound[r.Session]; !seen || r.Turn < t {
			firstBound[r.Session] = r.Turn
		}
	}
	for _, rec := range s.toolUse {
		if rec == nil || !IsPromptRecord(rec.ID, rec.Tool) || rec.ArgsDigest.IsZero() {
			continue
		}
		if first, bound := firstBound[rec.Session]; bound && rec.Turn >= first {
			continue
		}
		out[LegacyPromptKey{Session: rec.Session, Digest: rec.ArgsDigest}]++
	}
	return out
}

// ClaimLegacyPrompt reports whether an unpublished prompt capture of session sess, carrying payload,
// is accounted for by an earlier build's record counted in records, and consumes that record if so.
// The publication audit and `qompack fsck` both take records from LegacyPromptRecords and both claim
// through here, so the two cannot disagree.
func ClaimLegacyPrompt(records map[LegacyPromptKey]int, sess core.SessionID, payload []byte) bool {
	d, ok := LegacyPromptDigest(payload)
	if !ok {
		return false
	}
	k := LegacyPromptKey{Session: sess, Digest: d}
	if records[k] <= 0 {
		return false
	}
	records[k]--
	return true
}
