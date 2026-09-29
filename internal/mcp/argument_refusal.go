package mcp

// recallEmptyQueryMsg is recall's refusal of a query with nothing to search for. It matches what
// `qompack recall` with no query says, so the model and the command line learn one rule.
const recallEmptyQueryMsg = "recall requires a non-empty query: free text, or a selector with a value " +
	"(path:<glob>, symbol:<name>, tool:<name>)"

// alreadyTriedEmptyArgsMsg is already_tried's refusal of an empty target or approach.
const alreadyTriedEmptyArgsMsg = "already_tried requires a non-empty target and a non-empty approach"

// provenanceSearched names where a content-by-hash lookup looked before it could not establish the
// hash's provenance.
const provenanceSearched = "the root index, including every indexed root's chunk list"
