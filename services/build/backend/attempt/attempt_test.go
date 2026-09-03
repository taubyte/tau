// The tests are in attempt_test rather than in attempt, so that everything they
// can reach is everything a caller can reach: if one of these could be made or
// changed from outside, it would be here.
package attempt_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/build/backend/attempt"
)

// What one says is what it was made with, and there is no other way to make one.
func TestAnAttemptIsAJobAndARunOfIt(t *testing.T) {
	of := attempt.Nth("a job", 3)
	require.Equal(t, "a job", of.Job())
	require.Equal(t, uint32(3), of.Number())
}

// THE ZERO VALUE IS THE ONE A CALLER CAN WRITE FOR ITSELF, and it names no job:
// there is no exported field to put one in. Its caller composes a key from it,
// and a key built on an empty job is a row under an empty segment that no read
// of any job would reach — which is a refusal one floor up, never another job's
// rows.
func TestTheOnlyOneAcallerCanBuildForItselfNamesNoJob(t *testing.T) {
	var of attempt.Of
	require.Empty(t, of.Job())
	require.Zero(t, of.Number())
}

// A COPY IS THE SAME ATTEMPT AND CANNOT BE RE-POINTED. This is the whole reason
// the type is here rather than two fields in the caller: `other := *sink` is not
// a composite literal, so no census of literals sees it, and the round that
// moved a section's job id out of its handle learned that the hard way.
// Assigning through the copy is a compile error, which is what this test can
// only state — the two lines below do not compile:
//
//	other.job = "github.com/evil/repo"    unknown field job
//	attempt.Of{job: "…", n: 0}            unknown field job in struct literal
func TestACopyIsTheSameAttempt(t *testing.T) {
	of := attempt.Nth("a job", 1)
	other := of

	require.Equal(t, of, other)
	require.Equal(t, "a job", other.Job())

	// And two made the same way are equal, so a caller may compare them.
	require.Equal(t, of, attempt.Nth("a job", 1))
	require.NotEqual(t, of, attempt.Nth("a job", 2))
	require.NotEqual(t, of, attempt.Nth("another job", 1))
}
