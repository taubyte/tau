package primitives

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnAddressWithAHoleInItIsRefusedByWhatIsMissing(t *testing.T) {
	type refused struct {
		at   address
		want error
	}
	for _, c := range []refused{
		{address{environment: "prod", id: "q"}, ErrNoProject},
		{address{project: "p", id: "q"}, ErrNoEnvironment},
		{address{project: "p", environment: "prod"}, ErrNoID},
		{address{project: "p", environment: "prod", id: "q", instance: "i"}, ErrInstanceUnserved},
		{address{project: "p/x", environment: "prod", id: "q"}, ErrNoID},
		{address{project: "p", environment: "prod/x", id: "q"}, ErrNoID},
		{address{project: "p", environment: "prod", id: "q/x"}, ErrNoID},
	} {
		require.ErrorIs(t, c.at.valid(), c.want, "%+v", c.at)
	}
	require.NoError(t, address{project: "p", environment: "prod", id: "q"}.valid())

	_, err := addressFrom("p/prod")
	require.ErrorIs(t, err, ErrNoID)
	at, err := addressFrom("p/prod/q")
	require.NoError(t, err)
	require.Equal(t, address{project: "p", environment: "prod", id: "q"}, at)
}

func TestAPrefixReadsBackAsThePlaceItNames(t *testing.T) {
	s := Spec{Type: queueType, Project: "p", Environment: "prod", Name: "q"}
	got, ok := specFrom(s.Prefix())
	require.True(t, ok)
	require.Equal(t, s, got)

	for _, prefix := range []string{"queue/p/prod", "queue/p//q", "queue/p/prod/q/extra"} {
		_, ok := specFrom(prefix)
		require.False(t, ok, prefix)
	}
}

func TestAConsensusNobodyWroteIsTheOneThisServiceKeeps(t *testing.T) {
	require.Equal(t, consensusRaft, consensusOf(""))
	require.Equal(t, "pbft", consensusOf("pbft"))
}
