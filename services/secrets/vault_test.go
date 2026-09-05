package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func newVault(t *testing.T) *Vault {
	t.Helper()
	v, err := NewVault(NewMemStore())
	require.NoError(t, err)
	return v
}

func TestAVaultWithNoStoreHoldsNothing(t *testing.T) {
	_, err := NewVault(nil)
	require.ErrorIs(t, err, ErrNoStore)
}

func TestSetGetDelete(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	require.NoError(t, v.Set(ctx, "shop", "production", "DATABASE_URL", []byte("postgres://x")))

	value, err := v.Get(ctx, "shop", "production", "DATABASE_URL")
	require.NoError(t, err)
	require.Equal(t, "postgres://x", string(value))

	require.NoError(t, v.Delete(ctx, "shop", "production", "DATABASE_URL"))
	_, err = v.Get(ctx, "shop", "production", "DATABASE_URL")
	require.ErrorIs(t, err, ErrNotSet)
}

// TestRotatingIsSettingAgain, which is the whole reason the value is not in
// git: no commit, no build, no redeploy.
func TestRotatingIsSettingAgain(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	require.NoError(t, v.Set(ctx, "shop", "production", "TOKEN", []byte("old")))
	require.NoError(t, v.Set(ctx, "shop", "production", "TOKEN", []byte("new")))

	value, err := v.Get(ctx, "shop", "production", "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "new", string(value))
}

// TestDeletingWhatIsNotThereSucceeds. The caller wanted it gone and it is gone.
func TestDeletingWhatIsNotThereSucceeds(t *testing.T) {
	require.NoError(t, newVault(t).Delete(t.Context(), "shop", "production", "NEVER_SET"))
}

// TestAMissIsNotAnEmptyValue. A workload handed "" for its database password
// fails somewhere else, later, as an application that looks broken — so the
// two answers must be distinguishable, and an empty value that was genuinely
// SET must come back as one.
func TestAMissIsNotAnEmptyValue(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	_, err := v.Get(ctx, "shop", "production", "UNSET")
	require.ErrorIs(t, err, ErrNotSet)

	require.NoError(t, v.Set(ctx, "shop", "production", "EMPTY", nil))
	value, err := v.Get(ctx, "shop", "production", "EMPTY")
	require.NoError(t, err)
	require.Empty(t, value)

	// And through the worker's own door, where the distinction is what stops a
	// workload starting without its password.
	_, err = v.Secret(ctx, "shop", "production", "UNSET")
	require.ErrorIs(t, err, ErrNotSet)
}

// TestOneEnvironmentCannotReadAnother is the isolation the key exists for, and
// it is checked from both ends: a name that climbs, and a scope that is a
// prefix of a longer one.
func TestOneEnvironmentCannotReadAnother(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	require.NoError(t, v.Set(ctx, "shop", "production", "DATABASE_URL", []byte("the real one")))

	// A staging secret whose NAME is a path into production. Refused at the
	// grammar rather than joined into a key that resolves.
	err := v.Set(ctx, "shop", "staging", "../production/DATABASE_URL", []byte("nope"))
	require.ErrorIs(t, err, ErrSeparator)

	_, err = v.Get(ctx, "shop", "staging", "production/DATABASE_URL")
	require.ErrorIs(t, err, ErrSeparator)

	// An environment whose name is a prefix of another's. "prod" must not list
	// "production", which is what the trailing separator in the scope buys.
	require.NoError(t, v.Set(ctx, "shop", "prod", "OWN", []byte("x")))
	names, err := v.List(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, []string{"OWN"}, names)
}

func TestTheKeyGrammarRefusesAnEmptySegment(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	for _, c := range []struct {
		project, environment, name string
		want                       error
	}{
		{"", "production", "T", ErrNoProject},
		{"shop", "", "T", ErrNoEnvironment},
		{"shop", "production", "", ErrNoName},
	} {
		_, err := v.Get(ctx, c.project, c.environment, c.name)
		require.ErrorIs(t, err, c.want)
		require.ErrorIs(t, v.Set(ctx, c.project, c.environment, c.name, nil), c.want)
		require.ErrorIs(t, v.Delete(ctx, c.project, c.environment, c.name), c.want)
	}

	_, err := v.List(ctx, "", "production")
	require.ErrorIs(t, err, ErrNoProject)
	_, err = v.List(ctx, "shop", "")
	require.ErrorIs(t, err, ErrNoEnvironment)
	_, err = v.List(ctx, "shop", "a/b")
	require.ErrorIs(t, err, ErrSeparator)
}

// TestListIsNamesAndNeverValues. This is the difference between an operator
// checking whether DATABASE_URL is set and an operator exporting every secret
// the environment holds.
func TestListIsNamesAndNeverValues(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	require.NoError(t, v.Set(ctx, "shop", "production", "STRIPE", []byte("sk_live_dontleakme")))
	require.NoError(t, v.Set(ctx, "shop", "production", "DATABASE_URL", []byte("postgres://x")))
	require.NoError(t, v.Set(ctx, "shop", "staging", "STRIPE", []byte("sk_test")))

	names, err := v.List(ctx, "shop", "production")
	require.NoError(t, err)
	require.Equal(t, []string{"DATABASE_URL", "STRIPE"}, names, "sorted, and staging's is not here")

	// The compiler cannot check "no value leaked", so the assertion is on the
	// only thing that could carry one.
	for _, n := range names {
		require.NotContains(t, n, "sk_live", "a listing carried a value")
	}
}

func TestListOfAnEnvironmentWithNothingSetIsEmptyAndNotAnError(t *testing.T) {
	names, err := newVault(t).List(t.Context(), "shop", "production")
	require.NoError(t, err)
	require.Empty(t, names)
}

// TestAStoreThatCannotLookIsNotAMiss. Reporting a secret as unset while the
// swarm is unreachable would have an operator set it again, over the top of a
// value that was already there.
func TestAStoreThatCannotLookIsNotAMiss(t *testing.T) {
	v, err := NewVault(brokenStore{})
	require.NoError(t, err)

	_, err = v.Get(t.Context(), "shop", "production", "DATABASE_URL")
	require.ErrorIs(t, err, errUnreachable)
	require.NotErrorIs(t, err, ErrNotSet)

	_, err = v.List(t.Context(), "shop", "production")
	require.ErrorIs(t, err, errUnreachable)
}

var errUnreachable = errors.New("the store is unreachable")

// brokenStore reads and writes as a store on the far side of an unreachable
// swarm does: it fails.
type brokenStore struct{}

func (brokenStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errUnreachable
}
func (brokenStore) Put(context.Context, string, []byte) error      { return errUnreachable }
func (brokenStore) Delete(context.Context, string) error           { return errUnreachable }
func (brokenStore) List(context.Context, string) ([]string, error) { return nil, errUnreachable }

// TestTheKeyIsTheSecretsIdAndNotItsWrittenName. A reference compiles to an id,
// so this is what a worker and a build ask with — and the property is worth
// pinning because the alternative is silent: keying on the written name would
// let a rename in git detach a secret from its value, and nothing would fail
// until the workload that needed it started.
func TestTheKeyIsWhateverTheCompilerHandsIn(t *testing.T) {
	v := newVault(t)
	ctx := t.Context()

	const id = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"
	require.NoError(t, v.Set(ctx, "shop", "production", id, []byte("sk_live")))

	value, err := v.Secret(ctx, "shop", "production", id)
	require.NoError(t, err)
	require.Equal(t, "sk_live", value)

	// And the name it was written as resolves nothing here. Whoever types
	// "stripe" resolves it against the compiled project first.
	_, err = v.Secret(ctx, "shop", "production", "stripe")
	require.ErrorIs(t, err, ErrNotSet)
}
