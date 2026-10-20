package password

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/argon2"
)

// cheap keeps the tests fast; the format and the comparison do not depend on
// the cost.
var cheap = Params{Time: 1, Memory: 64, Threads: 1, KeyLen: 32}

func TestPepperedRoundTrip(t *testing.T) {
	pepper := []byte("pepper-one")
	h, err := New("correct horse", "p1", pepper, cheap)
	require.NoError(t, err)
	require.Equal(t, "p1", h.Pepper)
	require.NoError(t, h.Verify("correct horse", pepper))
	require.ErrorIs(t, h.Verify("correct horsf", pepper), ErrMismatch)
	// The same password under another pepper is another hash: a copied store
	// is useless without the pepper.
	require.ErrorIs(t, h.Verify("correct horse", []byte("pepper-two")), ErrMismatch)
	require.ErrorIs(t, h.Verify("correct horse", nil), ErrMismatch)
	require.ErrorIs(t, h.Verify(strings.Repeat("x", MaxLength+1), pepper), ErrMismatch)

	other, err := New("correct horse", "p1", pepper, cheap)
	require.NoError(t, err)
	require.NotEqual(t, h.Salt, other.Salt, "every hash is salted")
}

func TestCheck(t *testing.T) {
	require.Error(t, Check("short"))
	require.Error(t, Check(strings.Repeat("x", MaxLength+1)))
	require.NoError(t, Check("long enough"))
	_, err := New("short", "", nil, cheap)
	require.Error(t, err)
}

func TestParamsBounds(t *testing.T) {
	for _, p := range []Params{
		{Time: 0, Memory: 64, Threads: 1, KeyLen: 32},
		{Time: maxTime + 1, Memory: 64, Threads: 1, KeyLen: 32},
		{Time: 1, Memory: 4, Threads: 1, KeyLen: 32},
		{Time: 1, Memory: maxMemory + 1, Threads: 1, KeyLen: 32},
		{Time: 1, Memory: 64, Threads: 0, KeyLen: 32},
		{Time: 1, Memory: 64, Threads: 1, KeyLen: 8},
	} {
		_, err := New("long enough", "", nil, p)
		require.Error(t, err, "%+v", p)
		require.ErrorIs(t, Hash{Params: p, Sum: []byte{1}}.Verify("long enough", nil), ErrMismatch)
	}
	require.NoError(t, Default.valid())
}

func TestStale(t *testing.T) {
	h := Hash{Params: cheap}
	require.True(t, h.Stale(Default))
	require.False(t, Hash{Params: Default}.Stale(Default))
}

func TestPHC(t *testing.T) {
	h, err := New("admin password", "", nil, cheap)
	require.NoError(t, err)
	s, err := h.Encode()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(s, "$argon2id$v=19$m=64,t=1,p=1$"))
	back, err := Parse(s)
	require.NoError(t, err)
	require.Equal(t, h, back)
	require.NoError(t, back.Verify("admin password", nil))

	peppered, err := New("admin password", "p", []byte("x"), cheap)
	require.NoError(t, err)
	_, err = peppered.Encode()
	require.Error(t, err)

	// A PHC string written elsewhere verifies: it is plain argon2id over the
	// password, with the salt and parameters the string carries.
	raw := argon2.IDKey([]byte("password"), []byte("somesalt"), 2, 256, 4, 24)
	ref := "$argon2id$v=19$m=256,t=2,p=4$c29tZXNhbHQ$" + b64.EncodeToString(raw)
	got, err := Parse(ref)
	require.NoError(t, err)
	require.NoError(t, got.Verify("password", nil))
}

func TestParseRefuses(t *testing.T) {
	for _, s := range []string{
		"",
		"$argon2i$v=19$m=64,t=1,p=1$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=16$m=64,t=1,p=1$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m64,t=1,p=1$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=x,t=1,p=1$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,p=300$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,q=1$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$!!$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$!!",
		"$argon2id$v=19$m=64,t=1,p=1$c29tZXNhbHQ$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		_, err := Parse(s)
		require.Error(t, err, s)
	}
}
