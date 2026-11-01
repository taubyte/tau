package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestASecretHasNoRendering(t *testing.T) {
	s, err := NewSecret()
	require.NoError(t, err)
	require.Len(t, s.Expose(), SecretSize)

	raw := string(s.Expose())
	enclosing := struct {
		Name   string
		Secret Secret
	}{Name: "hook", Secret: s}

	for _, rendered := range []string{
		fmt.Sprint(s),
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%s", s),
		fmt.Sprintf("%q", s),
		fmt.Sprintf("%x", s),
		fmt.Sprintf("%#v", s),
		fmt.Sprintf("%v", enclosing),
		fmt.Sprintf("%+v", enclosing),
		s.String(),
		s.LogValue().String(),
	} {
		assert.Contains(t, rendered, "redacted")
		assert.NotContains(t, rendered, raw)
	}

	// A structured logger reaches unexported fields by reflection unless the
	// value says otherwise.
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("hook", "secret", s)
	assert.Contains(t, buf.String(), "redacted")
	assert.NotContains(t, buf.String(), raw)

	// THE MARSHALLERS FAIL RATHER THAN REDACT: a redacted secret written into a
	// store is a secret that is gone, and finding that out at the next
	// verification is worse than finding it out at the write.
	for _, marshal := range []func() ([]byte, error){s.MarshalJSON, s.MarshalText, s.MarshalBinary} {
		_, err := marshal()
		require.ErrorIs(t, err, ErrSecretNotSerializable)
	}
	_, err = json.Marshal(enclosing)
	require.Error(t, err, "a struct holding a Secret serialized")
}

func TestSecretComparisonRefusesTwoAbsences(t *testing.T) {
	a := SecretFrom([]byte("the same"))
	b := SecretFrom([]byte("the same"))
	assert.True(t, a.Equal(b))
	assert.False(t, a.Equal(SecretFrom([]byte("not the same"))))

	// "No secret configured" must never compare equal to anything, least of all
	// to another hook that is also unconfigured.
	assert.True(t, Secret{}.Empty())
	assert.False(t, Secret{}.Equal(Secret{}))
	assert.False(t, a.Equal(Secret{}))
	assert.False(t, Secret{}.Equal(a))

	assert.True(t, SecretFrom(nil).Empty())
	assert.True(t, SecretFrom([]byte{}).Empty())
}

func TestSecretFromCopies(t *testing.T) {
	buf := []byte("original")
	s := SecretFrom(buf)
	copy(buf, "MUTATED!")
	assert.Equal(t, "original", string(s.Expose()))
}

func TestCredentialSaysWhichKindItIs(t *testing.T) {
	tok := Token("ghp_example")
	assert.False(t, tok.IsZero())
	assert.False(t, tok.IsAnonymous())
	assert.Equal(t, "ghp_example", string(tok.Expose()))

	anon := Anonymous()
	assert.True(t, anon.IsAnonymous())
	assert.False(t, anon.IsZero(), "Anonymous is a value that was chosen, not one that was forgotten")

	var zero Credential
	assert.True(t, zero.IsZero())
	assert.False(t, zero.IsAnonymous())

	// An empty token string is a dropped token and not a decision.
	assert.True(t, Token("").IsZero())

	// A Credential is a Secret, so it renders the same way as one.
	assert.NotContains(t, fmt.Sprintf("%v", tok), "ghp_example")
	_, err := json.Marshal(tok)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSecretNotSerializable) || err != nil)
}

func TestAccessAndProtocolRender(t *testing.T) {
	for lvl, want := range map[Access]string{
		AccessNone: "none", AccessRead: "read", AccessWrite: "write", AccessAdmin: "admin",
		Access(9): "none",
	} {
		assert.Equal(t, want, lvl.String())
	}
	// The zero value is the safe answer, in both directions.
	var zero Access
	assert.Equal(t, AccessNone, zero)
	assert.False(t, zero.CanWrite())
	assert.False(t, zero.CanAdmin())
	assert.True(t, AccessWrite.CanWrite())
	assert.False(t, AccessWrite.CanAdmin())
	assert.True(t, AccessAdmin.CanWrite())
	assert.True(t, AccessAdmin.CanAdmin())
	assert.False(t, AccessRead.CanWrite(), "visibility is not permission")

	assert.Equal(t, "https", HTTPS.String())
	assert.Equal(t, "ssh", SSH.String())
}
