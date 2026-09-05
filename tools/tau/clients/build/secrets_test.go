package build

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestASecretRoundTripsAsThreeCallsAndNoFourth. There is no Get here because
// there is no route to call: the frontend never returns a value.
func TestSecretsListSetDelete(t *testing.T) {
	var got struct {
		method string
		path   string
		body   string
	}

	c, f := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		body, _ := io.ReadAll(r.Body)
		got.body = string(body)

		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]string{"QmOne", "QmTwo"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	names, err := c.Secrets(t.Context(), "proj", "production")
	require.NoError(t, err)
	require.Equal(t, []string{"QmOne", "QmTwo"}, names)
	require.Equal(t, "/secrets/proj/production", got.path)

	require.NoError(t, c.SetSecret(t.Context(), "proj", "production", "QmOne", []byte("sk_live")))
	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/secrets/proj/production/QmOne", got.path)
	require.Equal(t, "sk_live", got.body)

	require.NoError(t, c.DeleteSecret(t.Context(), "proj", "production", "QmOne"))
	require.Equal(t, http.MethodDelete, got.method)

	for _, path := range f.seen() {
		require.True(t, strings.HasPrefix(path, "/secrets/"), path)
	}
}

// TestA204IsASuccess. The write routes answer with no body, and a client that
// only accepted 200 would report a rotation that happened as one that did not
// — so the operator would set it again and never know which value is live.
func TestANoContentIsASuccess(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, c.SetSecret(t.Context(), "proj", "production", "QmOne", []byte("v")))
	require.NoError(t, c.DeleteSecret(t.Context(), "proj", "production", "QmOne"))
}

// TestAValueWithAnythingInItSurvives. The body is the value, raw — not a JSON
// field somebody has to escape correctly.
func TestAValueIsSentRaw(t *testing.T) {
	const awkward = "line one\nline two\t\"quoted\"\x00\xff"

	var sent string
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent = string(body)
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.SetSecret(t.Context(), "proj", "production", "QmOne", []byte(awkward)))
	require.Equal(t, awkward, sent)
}

// TestASegmentIsEscaped, so a name with a slash in it cannot become a path.
func TestPathSegmentsAreEscaped(t *testing.T) {
	c, f := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	require.NoError(t, c.SetSecret(t.Context(), "proj", "pro/duction", "a/b", []byte("v")))
	require.Equal(t, []string{"/secrets/proj/pro%2Fduction/a%2Fb"}, f.seen())
}

func TestASecretCallNeedsItsThreeParts(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) { t.Fatal("nothing should be sent") })

	_, err := c.Secrets(t.Context(), "", "production")
	require.ErrorContains(t, err, "no project")
	_, err = c.Secrets(t.Context(), "proj", "")
	require.ErrorContains(t, err, "no environment")
	require.ErrorContains(t, c.SetSecret(t.Context(), "proj", "production", "", nil), "no secret name")
	require.ErrorContains(t, c.DeleteSecret(t.Context(), "proj", "production", ""), "no secret name")
}

// TestAnOversizedValueIsNamed, so the message is "that is bigger than a secret
// may be" rather than a bare 413.
func TestATooLargeValueIsNamed(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})

	err := c.SetSecret(t.Context(), "proj", "production", "QmOne", []byte("v"))
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestADeniedSecretCallIsNotFound(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	err := c.SetSecret(t.Context(), "proj", "production", "QmOne", []byte("v"))
	require.ErrorIs(t, err, ErrNotFound)
}
