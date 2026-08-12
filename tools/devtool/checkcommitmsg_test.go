package main

import (
	"strings"
	"testing"
)

// TestCheckCommitMsg is the table-driven test the implementation spec's Test plan names
// explicitly: valid subjects, an over-length subject, a trailing period, a missing type, a
// Co-Authored-By line in the body, the robot emoji in the body, and a missing Refs: footer on a
// feat commit.
func TestCheckCommitMsg(t *testing.T) {
	cases := []struct {
		name    string
		msg     string
		wantErr bool
	}{
		{
			name: "valid feat with scope and Refs footer",
			msg:  "feat(store): add content-addressed object writer\n\nRefs: SP-06, §5.8\n",
		},
		{
			name: "valid fix without scope and Refs footer",
			msg:  "fix: correct off-by-one in chunk boundary\n\nRefs: SP-04, G3.1\n",
		},
		{
			name: "valid docs commit needs no Refs footer",
			msg:  "docs: clarify config precedence order\n",
		},
		{
			// A foundation commit legitimately lands several packages at once, and §10 lets the
			// scope name the Go packages. Rejecting a comma would force those commits to fall back
			// to a subplan slug, which says strictly less about what changed.
			name: "valid multi-package scope",
			msg:  "feat(core,paths): cross-cutting primitives and the append-only guard\n\nRefs: SP-01, §7.4\n",
		},
		{
			name: "valid four-package scope",
			msg:  "feat(logging,obs,tokens,hookio): Loud channel and log-bucket histograms\n\nRefs: SP-01, §11.3\n",
		},
		{
			name:    "scope may not contain spaces",
			msg:     "feat(core paths): add the thing\n\nRefs: SP-01\n",
			wantErr: true,
		},
		{
			name: "valid chore commit with a body that wraps",
			msg: "chore: tidy go.sum\n\n" +
				"This line is well under the one hundred character limit that the body text must respect.\n",
		},
		{
			name:    "over-length subject",
			msg:     "feat: " + strings.Repeat("a", 65) + "\n\nRefs: SP-01\n",
			wantErr: true,
		},
		{
			name:    "trailing period on subject",
			msg:     "feat: add the thing.\n\nRefs: SP-01\n",
			wantErr: true,
		},
		{
			name:    "missing type prefix",
			msg:     "add the thing\n",
			wantErr: true,
		},
		{
			name:    "unknown type prefix",
			msg:     "oops: add the thing\n",
			wantErr: true,
		},
		{
			name:    "Co-Authored-By in body",
			msg:     "feat: add the thing\n\nRefs: SP-01\nCo-Authored-By: Someone <someone@example.com>\n",
			wantErr: true,
		},
		{
			name:    "Signed-off-by in body",
			msg:     "feat: add the thing\n\nRefs: SP-01\nSigned-off-by: Someone <someone@example.com>\n",
			wantErr: true,
		},
		{
			name:    "Generated with in body",
			msg:     "feat: add the thing\n\nGenerated with some tool\n\nRefs: SP-01\n",
			wantErr: true,
		},
		{
			name:    "robot emoji in body",
			msg:     "feat: add the thing\n\n\U0001F916 built by a robot\n\nRefs: SP-01\n",
			wantErr: true,
		},
		{
			name:    "missing Refs footer on a feat commit",
			msg:     "feat: add the thing\n\nNo footer here.\n",
			wantErr: true,
		},
		{
			name:    "missing Refs footer on a fix commit",
			msg:     "fix: correct the thing\n",
			wantErr: true,
		},
		{
			name:    "empty message",
			msg:     "",
			wantErr: true,
		},
		{
			name:    "body line over 100 characters",
			msg:     "feat: add the thing\n\n" + strings.Repeat("a", 101) + "\n\nRefs: SP-01\n",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCommitMsg(c.msg)
			if c.wantErr && err == nil {
				t.Errorf("validateCommitMsg(%q) = nil, want an error", c.msg)
			}
			if !c.wantErr && err != nil {
				t.Errorf("validateCommitMsg(%q) = %v, want nil", c.msg, err)
			}
		})
	}
}
