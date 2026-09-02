package frontend

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// push.go is a pure function of the bytes, so its cases are testable without a
// server — which is the whole reason the provider's shape is confined to one
// file.

func TestParsePushReadsThreeFieldsAndNothingElse(t *testing.T) {
	body := pushBody("https://github.com/Acme/Pay.git", "refs/heads/main", testSHA)

	got, err := parsePush(body)

	require.NoError(t, err)
	require.Equal(t, testRepoKey, got.Repository.String())
	require.Equal(t, "refs/heads/main", got.Ref)
	require.Equal(t, testSHA, got.CommitSHA)
}

// A DELETED BRANCH IS NOT A BUILD, and it is the one payload shape that would
// otherwise sail through every check: `after` is forty zeros, which is a
// perfectly well-formed lowercase object name, so the backend would accept it
// and queue a build of a ref that no longer exists.
func TestADeletedRefIsNotABuild(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() []byte
	}{
		{"deleted flag", func() []byte {
			var payload map[string]any
			json.Unmarshal(pushBody("https://github.com/acme/pay", "refs/heads/main", testSHA), &payload)
			payload["deleted"] = true
			b, _ := json.Marshal(payload)
			return b
		}},
		{"zero after", func() []byte {
			return pushBody("https://github.com/acme/pay", "refs/heads/main", zeroSHA)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePush(tc.build())
			var decided *httpError
			require.ErrorAs(t, err, &decided)
			require.Equal(t, http.StatusAccepted, decided.status)
		})
	}
}

func TestParsePushRefusesAPayloadItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"not json", []byte("{"), "malformed payload"},
		{"no repository", pushBody("", "refs/heads/main", testSHA), "names no repository"},
		{"unparseable clone url", pushBody("not a url at all", "refs/heads/main", testSHA), "names no repository"},
		{"no ref", pushBody("https://github.com/acme/pay", "", testSHA), "names no ref"},
		{"a tag, not a ref", pushBody("https://github.com/acme/pay", "main", testSHA), "names no ref"},
		{"no commit", pushBody("https://github.com/acme/pay", "refs/heads/main", ""), "names no commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePush(tc.body)
			var decided *httpError
			require.ErrorAs(t, err, &decided)
			require.Equal(t, http.StatusBadRequest, decided.status)
			require.Contains(t, decided.msg, tc.want)
		})
	}
}

// A tag push carries refs/tags/…, which is not a branch and is not in any
// eligible list. It is refused by the ref policy rather than by a second check.
func TestATagPushIsIgnoredByPolicy(t *testing.T) {
	h := newHarness(t, Config{})
	body := pushBody("https://github.com/acme/pay", "refs/tags/v1.0.0", testSHA)

	got, err := parsePush(body)
	require.NoError(t, err)
	require.False(t, h.eligible(got.Ref))
}

func TestEligibilityIsExactAndNotAPrefix(t *testing.T) {
	h := newHarness(t, Config{EligibleRefs: []string{"refs/heads/main"}})

	require.True(t, h.eligible("refs/heads/main"))
	require.False(t, h.eligible("refs/heads/mainline"))
	require.False(t, h.eligible("refs/heads/main/x"))
	require.False(t, h.eligible("Refs/Heads/Main"))
	require.False(t, h.eligible(""))
}
