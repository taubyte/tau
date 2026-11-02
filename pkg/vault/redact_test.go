package vault

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func written(r *Redactor, out *bytes.Buffer, chunks ...string) string {
	for _, c := range chunks {
		r.Write([]byte(c))
	}
	r.Flush()
	return out.String()
}

func TestNothingToMaskPassesStraightThrough(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(&out)
	r.Write([]byte("building\n"))
	require.Equal(t, "building\n", out.String(), "nothing held when nothing is masked")
}

func TestAValueIsMaskedWhereverItFalls(t *testing.T) {
	type testCase struct {
		name   string
		chunks []string
		want   string
	}
	for _, tc := range []testCase{
		{"whole", []string{"token=sk_live_1\n"}, "token=***\n"},
		{"split across writes", []string{"token=sk_", "li", "ve_1 done\n"}, "token=*** done\n"},
		{"twice", []string{"sk_live_1 sk_live_1"}, "*** ***"},
		{"at the very end", []string{"x sk_live", "_1"}, "x ***"},
		{"a prefix that is not one", []string{"sk_live_", "2\n"}, "sk_live_2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			r := NewRedactor(&out)
			r.Add([]byte("sk_live_1"))
			require.Equal(t, tc.want, written(r, &out, tc.chunks...))
		})
	}
}

func TestEachLineOfAMultiLineValueIsMasked(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(&out)
	r.Add([]byte("-----BEGIN KEY-----\r\nMIIEvQIBADANBg\n-----END KEY-----\n"))

	got := written(r, &out, "key line: MIIEvQIBADANBg\n")
	require.Equal(t, "key line: ***\n", got)
}

func TestAValueInsideALongerOneLeavesNoneOfTheLongerInTheClear(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(&out)
	r.Add([]byte("abc"))
	r.Add([]byte("abcdef"))

	require.Equal(t, "<***>", written(r, &out, "<abcdef>"))
}

type failing struct{}

var errFull = errors.New("log full")

func (failing) Write([]byte) (int, error) { return 0, errFull }

func TestAFailingLogIsReported(t *testing.T) {
	r := NewRedactor(failing{})
	r.Add([]byte("s3cr3t"))
	_, err := r.Write([]byte("long enough to be written past the hold\n"))
	require.ErrorIs(t, err, errFull)
}
