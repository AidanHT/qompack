package modwire_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/modwire"
)

const validBrief = `{"v":1,"session_id":"s-1","cwd":"/p","trigger":"manual",` +
	`"user_instructions_present":false,"max_chars":100}`

func TestDecode_UnknownFieldsAreIgnored(t *testing.T) {
	in := `{"v":1,"session_id":"s-1","cwd":"/p","trigger":"auto","user_instructions_present":false,` +
		`"max_chars":10,"future_field":{"nested":[1,2,3]},"another":"x"}`
	got, err := modwire.DecodeBriefRequest(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, "s-1", got.SessionID)
	require.Equal(t, 10, got.MaxChars)

	hello := `{"v":1,"session_id":"s-1","cwd":"/p","host_version":"2.1.300","surface":"terminal"}`
	h, err := modwire.DecodeHelloRequest(strings.NewReader(hello))
	require.NoError(t, err)
	require.Equal(t, "2.1.300", h.HostVersion)
}

func TestDecode_UnknownVersionIsRefusedWithAnErrorObject(t *testing.T) {
	for _, in := range []string{
		`{"v":2,"session_id":"s-1","cwd":"/p","trigger":"auto","max_chars":1}`,
		`{"v":0,"session_id":"s-1","cwd":"/p","trigger":"auto"}`,
		`{"session_id":"s-1","cwd":"/p","trigger":"auto"}`,
	} {
		_, err := modwire.DecodeBriefRequest(strings.NewReader(in))
		require.ErrorIs(t, err, modwire.ErrUnsupportedVersion, in)

		st := modwire.FailureFor(err)
		require.Equal(t, modwire.Version, st.V, "the refusal speaks the version this side knows")
		require.False(t, st.OK)
		require.Equal(t, modwire.ReasonError, st.Reason)
		require.NotEmpty(t, st.Detail)
	}

	var ve *modwire.VersionError
	_, err := modwire.DecodeHelloRequest(strings.NewReader(`{"v":7,"session_id":"s","cwd":"/p","host_version":"x"}`))
	require.True(t, errors.As(err, &ve))
	require.Equal(t, 7, ve.Got)
}

func TestDecode_MalformedInputIsAnError(t *testing.T) {
	for _, in := range []string{
		``,
		`not json`,
		`[1,2]`,
		`{"v":1,"session_id":"s-1","cwd":"/p","trigger":"auto"} {"v":1}`,
		`{"v":"1","session_id":"s-1","cwd":"/p","trigger":"auto"}`,
		`{"v":1,"session_id":"s-1","cwd":"/p","trigger":"auto","max_chars":"ten"}`,
	} {
		_, err := modwire.DecodeBriefRequest(strings.NewReader(in))
		require.ErrorIs(t, err, modwire.ErrMalformed, "%q", in)
		require.Equal(t, modwire.ReasonError, modwire.FailureFor(err).Reason)
	}
}

func TestDecode_OversizedInputIsRefused(t *testing.T) {
	pad := strings.Repeat("x", modwire.MaxMessageBytes)
	in := `{"v":1,"session_id":"s-1","cwd":"/p","trigger":"auto","pad":"` + pad + `"}`
	_, err := modwire.DecodeBriefRequest(strings.NewReader(in))
	require.ErrorIs(t, err, modwire.ErrTooLarge)
}

func TestDecode_FieldValidation(t *testing.T) {
	cases := map[string]string{
		"empty session":       `{"v":1,"session_id":"","cwd":"/p","trigger":"auto"}`,
		"traversal session":   `{"v":1,"session_id":"../x","cwd":"/p","trigger":"auto"}`,
		"separator session":   `{"v":1,"session_id":"a/b","cwd":"/p","trigger":"auto"}`,
		"backslash session":   `{"v":1,"session_id":"a\\b","cwd":"/p","trigger":"auto"}`,
		"dot session":         `{"v":1,"session_id":"..","cwd":"/p","trigger":"auto"}`,
		"empty cwd":           `{"v":1,"session_id":"s","cwd":"","trigger":"auto"}`,
		"nul in cwd":          `{"v":1,"session_id":"s","cwd":"/p\u0000q","trigger":"auto"}`,
		"empty trigger":       `{"v":1,"session_id":"s","cwd":"/p","trigger":""}`,
		"odd trigger":         `{"v":1,"session_id":"s","cwd":"/p","trigger":"Auto Mode"}`,
		"negative max_chars":  `{"v":1,"session_id":"s","cwd":"/p","trigger":"auto","max_chars":-1}`,
		"overlong session id": `{"v":1,"session_id":"` + strings.Repeat("a", modwire.MaxSessionIDBytes+1) + `","cwd":"/p","trigger":"auto"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := modwire.DecodeBriefRequest(strings.NewReader(in))
			require.ErrorIs(t, err, modwire.ErrInvalid)
			require.Equal(t, modwire.ReasonError, modwire.FailureFor(err).Reason)
		})
	}

	_, err := modwire.DecodeHelloRequest(strings.NewReader(`{"v":1,"session_id":"s","cwd":"/p","host_version":""}`))
	require.ErrorIs(t, err, modwire.ErrInvalid, "a hello without a host version cannot be gated")

	_, err = modwire.DecodeBriefRequest(strings.NewReader(validBrief))
	require.NoError(t, err)
}

func TestBriefRequest_EffectiveMaxChars(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, modwire.BriefMaxChars},
		{1, 1},
		{modwire.BriefMaxChars - 1, modwire.BriefMaxChars - 1},
		{modwire.BriefMaxChars, modwire.BriefMaxChars},
		{modwire.BriefMaxChars + 1, modwire.BriefMaxChars},
	}
	for _, c := range cases {
		require.Equal(t, c.want, modwire.BriefRequest{MaxChars: c.in}.EffectiveMaxChars(), "max_chars %d", c.in)
	}
}

func TestCharCount_IsUTF16CodeUnits(t *testing.T) {
	require.Equal(t, 0, modwire.CharCount(""))
	require.Equal(t, 5, modwire.CharCount("hello"))
	require.Equal(t, 1, modwire.CharCount("é"), "a BMP rune is one unit")
	require.Equal(t, 2, modwire.CharCount("😀"), "an astral rune is a surrogate pair, as JS length counts it")
	require.Equal(t, 1, modwire.CharCount("\xff"), "invalid UTF-8 decodes to U+FFFD, one unit")
}

func TestStatus_ServedAndFailure(t *testing.T) {
	s := modwire.Served()
	require.Equal(t, modwire.Status{V: modwire.Version, OK: true}, s)

	f := modwire.Failure(modwire.ReasonBusy, "accept deadline passed")
	require.Equal(t, modwire.Status{V: modwire.Version, Reason: modwire.ReasonBusy, Detail: "accept deadline passed"}, f)
}

func TestEncode_BriefResponseAlwaysCarriesInstructionsAndFactIDs(t *testing.T) {
	got, err := modwire.Encode(modwire.BriefResponse{Status: modwire.Failure(modwire.ReasonTimeout, "")})
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"ok":false,"reason":"timeout","instructions":"","fact_ids":[]}`+"\n", string(got))
}

func TestEncode_RefusesAnOversizedMessage(t *testing.T) {
	_, err := modwire.Encode(modwire.BriefResponse{
		Status:       modwire.Served(),
		Instructions: strings.Repeat("y", modwire.MaxMessageBytes),
	})
	require.ErrorIs(t, err, modwire.ErrTooLarge)
}

func TestBriefResponse_Validate(t *testing.T) {
	ok := modwire.BriefResponse{Status: modwire.Served(), Instructions: "abc", FactIDs: []string{"f1"}}
	require.NoError(t, ok.Validate(3))
	require.ErrorIs(t, ok.Validate(2), modwire.ErrInvalid, "instructions over max_chars")

	bad := []modwire.BriefResponse{
		{Status: modwire.Status{V: 2, OK: true}},
		{Status: modwire.Status{V: 1, OK: true, Reason: modwire.ReasonCold}},
		{Status: modwire.Status{V: 1, OK: false}},
		{Status: modwire.Status{V: 1, OK: false, Reason: "sleepy"}},
		{Status: modwire.Failure(modwire.ReasonCold, ""), Instructions: "served anyway"},
		{Status: modwire.Served(), FactIDs: []string{""}},
		{Status: modwire.Served(), FactIDs: []string{strings.Repeat("f", modwire.MaxFactIDBytes+1)}},
		{Status: modwire.Served(), FactIDs: make([]string, modwire.MaxFactIDs+1)},
	}
	for i, r := range bad {
		require.ErrorIs(t, r.Validate(modwire.BriefMaxChars), modwire.ErrInvalid, "case %d", i)
	}
}

// TestLimits_TheLargestValidBriefFitsOneMessage is the arithmetic the limits must satisfy: a
// response at every bound at once — max_chars of the costliest-to-escape character, the longest
// detail of the same, the most fact IDs at their longest built from every byte the fact-id
// alphabet allows — still encodes under MaxMessageBytes. If a later change raises a bound or
// widens an alphabet without raising the frame, this fails before a real brief is truncated by it.
func TestLimits_TheLargestValidBriefFitsOneMessage(t *testing.T) {
	alphabet := allowedBytes(t, modwire.FactIDChars)
	ids := make([]string, modwire.MaxFactIDs)
	for i := range ids {
		id := make([]byte, modwire.MaxFactIDBytes)
		for j := range id {
			id[j] = alphabet[(i*modwire.MaxFactIDBytes+j)%len(alphabet)]
		}
		ids[i] = string(id)
	}
	worst := modwire.BriefResponse{
		Status:       modwire.Served(),
		Instructions: strings.Repeat("\x01", modwire.BriefMaxChars), // escapes to \u0001: 6 bytes each
		FactIDs:      ids,
	}
	worst.Detail = strings.Repeat("\x01", modwire.MaxDetailBytes)
	require.NoError(t, worst.Validate(modwire.BriefMaxChars))
	b, err := modwire.Encode(worst)
	require.NoError(t, err)
	require.LessOrEqual(t, len(b), modwire.MaxMessageBytes)
}

func TestVocabulary(t *testing.T) {
	require.Equal(t, []modwire.Verb{modwire.VerbHello, modwire.VerbBrief}, modwire.Verbs())
	require.Equal(t, []modwire.Reason{
		modwire.ReasonCold, modwire.ReasonBusy, modwire.ReasonTimeout, modwire.ReasonRefused, modwire.ReasonError,
	}, modwire.Reasons())
	require.Equal(t, []modwire.Outcome{
		modwire.OutcomeServed, modwire.OutcomeCold, modwire.OutcomeBusy, modwire.OutcomeTimeout,
		modwire.OutcomeRefused, modwire.OutcomeError,
	}, modwire.Outcomes())

	// Every unserved outcome is spelled exactly as the reason that produced it.
	for _, r := range modwire.Reasons() {
		require.Equal(t, string(r), string(modwire.OutcomeFor(modwire.Failure(r, ""))))
	}
	require.Equal(t, modwire.OutcomeServed, modwire.OutcomeFor(modwire.Served()))
	require.Equal(t, modwire.OutcomeError, modwire.OutcomeFor(modwire.Status{V: 1, Reason: "unknown"}))
}
