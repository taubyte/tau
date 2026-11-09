package projects

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

func TestEveryRefusalComesBackAsItsSentinel(t *testing.T) {
	type testCase struct {
		sentinel error
		code     connect.Code
	}
	for _, tc := range []testCase{
		{ErrNoProject, connect.CodeInvalidArgument},
		{ErrSeparator, connect.CodeInvalidArgument},
		{ErrNoRepository, connect.CodeInvalidArgument},
		{ErrNoClaimant, connect.CodeInvalidArgument},
		{ErrNoRole, connect.CodeInvalidArgument},
		{ErrNoGrantee, connect.CodeInvalidArgument},
		{ErrNotLinked, connect.CodeNotFound},
		{ErrNoGrant, connect.CodeNotFound},
		{ErrProjectClaimed, connect.CodeAlreadyExists},
		{ErrRepositoryClaimed, connect.CodeAlreadyExists},
	} {
		onWire := wire(fmt.Errorf("%w: detail", tc.sentinel))
		require.Equal(t, tc.code, connect.CodeOf(onWire), tc.sentinel.Error())
		require.ErrorIs(t, unwire(onWire), tc.sentinel)
	}
}

func TestAStoreFailureIsUnavailableAndNamesNoSentinel(t *testing.T) {
	onWire := wire(errors.New("auth: reading /projects/claim/x: disk gone"))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(onWire), "a caller retries elsewhere")
	back := unwire(onWire)
	require.Same(t, onWire, back)

	plain := errors.New("not a status")
	require.Same(t, plain, unwire(plain))
	require.Nil(t, unwire(nil))
}
