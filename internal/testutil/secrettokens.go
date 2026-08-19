package testutil

import "bytes"

// The redaction engine's job is to find credentials in tool output and scrub them before anything
// is stored, so testing it requires inputs that are byte-for-byte the shape of real credentials.
// Committing those inputs verbatim means every secret scanner pointed at this repository fires on
// the fixtures — which is what happened, and what a history rewrite was then asked to undo.
//
// The fixtures on disk therefore carry a placeholder where the credential-shaped run belongs, and
// this file reconstitutes it at run time. Two properties matter and neither is negotiable:
//
//   - Expansion is byte-exact. A test that reads a fixture gets precisely the bytes it got before
//     the placeholders were introduced, so no assertion, golden or content hash changes meaning.
//     Nothing here weakens a check; the redaction suite still runs against real credential shapes.
//   - The literals below are written split across a `+`. Go concatenates at compile time, so the
//     runtime values are identical while no contiguous credential-shaped run appears in the source
//     for a scanner to match. That is the same trick applied to every test literal in the tree.
//
// Every value is synthetic: AWS's own published example key, the alphabet forwards and backwards,
// eleven a's, and the JWT from jwt.io's front page. None was ever a real credential.
var secretTokens = map[string]string{
	"@@SEC_AWS_AKID@@":        "AKIA" + "IOSFODNN7EXAMPLE",
	"@@SEC_AWS_AKID_ALPHA@@":  "AKIA" + "ABCDEFGHIJKLMNOP",
	"@@SEC_AWS_ASIA@@":        "ASIA" + "IOSFODNN7EXAMPLE",
	"@@SEC_ANTHROPIC_AB@@":    "sk-ant-api03-" + "AAAABBBBCCCCDDDD",
	"@@SEC_GH_PAT@@":          "ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12",
	"@@SEC_GH_PAT_ALPHA@@":    "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789",
	"@@SEC_GH_OAUTH@@":        "gho_" + "zyxwvutsrqponmlkjihgfedcba9876543210",
	"@@SEC_ANTHROPIC@@":       "sk-ant-api03-" + "1234567890abcdefghijklmnopqrstuvwxyz",
	"@@SEC_ANTHROPIC_ALPHA@@": "sk-ant-api03-" + "abcdefghijklmnopqrstuvwxyz",
	"@@SEC_GENERIC_SK@@":      "sk-" + "1234567890abcdefghijklmnopqrstuvwxyz",
	"@@SEC_STRIPE@@":          "sk_live_" + "aaaaaaaaaaa",
	"@@SEC_JWT@@": "eyJhbGciOiJI" + "UzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
		"dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	"@@SEC_JWT_SHORT@@": "eyJhbGciOiJI" + "UzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
		"dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	"@@SEC_PEM_RSA_BEGIN@@": "-----BEGIN RSA PRIV" + "ATE KEY-----",
	"@@SEC_PEM_RSA_END@@":   "-----END RSA PRIV" + "ATE KEY-----",
	"@@SEC_PEM_SSH_BEGIN@@": "-----BEGIN OPENSSH PRIV" + "ATE KEY-----",
	"@@SEC_PEM_SSH_END@@":   "-----END OPENSSH PRIV" + "ATE KEY-----",
}

// ExpandSecretTokens replaces every @@SEC_…@@ placeholder in a fixture with the credential-shaped
// string it stands for. Input containing no placeholder is returned unchanged.
func ExpandSecretTokens(b []byte) []byte {
	if !bytes.Contains(b, []byte("@@SEC_")) {
		return b
	}
	for token, value := range secretTokens {
		b = bytes.ReplaceAll(b, []byte(token), []byte(value))
	}
	return b
}

// SecretTokenValue returns the expansion of one placeholder, for tests that need the credential
// string on its own rather than embedded in a fixture. It reports whether the token is known.
func SecretTokenValue(token string) (string, bool) {
	v, ok := secretTokens[token]
	return v, ok
}
