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
// digest bound to the delivery's observation, so an unbound digest identifies an earlier build's
// record.
//
// A released build never wrote a sidecar at all: v0.2.0 has no records/captures/ tree, and a store
// it wrote carries nothing this file reads.
//
// Such a sidecar's reference EXISTS, so it is not a publication gap, and the accounting reads it as
// published: the sidecar's own payload names the prompt, its session names the record's session, and
// a record in that session carrying that digest is its reference. Each record accounts for one
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

// legacyPromptRecords counts this store's prompt records by (session, args digest). A current
// build's leased prompt record carries an observation-bound digest no earlier build's sidecar
// produces, so counting every prompt record is safe: only an unbound digest can be matched.
func (s *FSStore) legacyPromptRecords() map[LegacyPromptKey]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[LegacyPromptKey]int{}
	for _, rec := range s.toolUse {
		if rec != nil && IsPromptRecord(rec.ID, rec.Tool) && !rec.ArgsDigest.IsZero() {
			out[LegacyPromptKey{Session: rec.Session, Digest: rec.ArgsDigest}]++
		}
	}
	return out
}

// ClaimLegacyPrompt reports whether an unpublished prompt capture of session sess, carrying payload,
// is accounted for by an earlier build's record counted in records, and consumes that record if so.
// The publication audit counts the records from the store; `qompack fsck` counts them from
// index/tool_use.jsonl, and both claim through here so the two cannot disagree.
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
