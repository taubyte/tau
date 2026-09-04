package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// recordingMeter is a sink that keeps what it was handed.
type recordingMeter struct{ events []Accounted }

func (m *recordingMeter) Record(_ context.Context, ev Accounted) {
	m.events = append(m.events, ev)
}

var _ Meter = (*recordingMeter)(nil)

// TestAnAccountedWriteIsAlsoAnEmission is the whole point of meter.go: the
// counter and the event describe the same operation, and the event carries the
// tenant the counter cannot.
func TestAnAccountedWriteIsAlsoAnEmission(t *testing.T) {
	ctx := context.Background()
	m := &recordingMeter{}
	i := index{store: newOrderedMem(), meter: m}
	a := addr(t)

	require.NoError(t, i.Add(ctx, a, "peerA", 100))
	require.NoError(t, i.Remove(ctx, a, "peerA", 40))

	require.Len(t, m.events, 2)

	require.Equal(t, OpAdd, m.events[0].Op)
	require.Equal(t, uint64(100), m.events[0].Bytes)
	require.Equal(t, "peerA", m.events[0].Writer)
	require.Equal(t, a.Bucket().Project(), m.events[0].Project)
	require.Equal(t, a.Bucket().Environment(), m.events[0].Environment,
		"environment is a tenancy axis, so an event without it cannot be attributed")
	require.Equal(t, a.Bucket().Name(), m.events[0].Bucket)
	require.Equal(t, a.Instance(), m.events[0].Instance)

	require.Equal(t, OpRemove, m.events[1].Op)
	require.Equal(t, uint64(40), m.events[1].Bytes)

	// And the counter still says what it always said.
	u, err := i.Usage(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(60), u.Used())
}

// TestAWriteThatFailedIsNotEmitted. An emission is a claim that something
// happened. If it is built before the row moves, or from a path that returns
// an error, a sink totals writes the store never took.
func TestAWriteThatFailedIsNotEmitted(t *testing.T) {
	ctx := context.Background()
	m := &recordingMeter{}
	i := index{store: failingStore{}, meter: m}

	require.Error(t, i.Add(ctx, addr(t), "peerA", 100))
	require.Error(t, i.Remove(ctx, addr(t), "peerA", 100))
	require.Empty(t, m.events, "the store refused both, so nothing was accounted and nothing happened")
}

// TestOnlyARefusalIsEmittedByAdmit. Admit is a question, and asking it is not
// an event. Being turned away is, and it is the one the rows cannot show —
// they look identical either side of a refusal.
func TestOnlyARefusalIsEmittedByAdmit(t *testing.T) {
	ctx := context.Background()
	m := &recordingMeter{}
	i := index{store: newOrderedMem(), meter: m}
	a := addr(t)

	require.NoError(t, i.Add(ctx, a, "peerA", 900))
	m.events = nil

	ok, _, err := i.Admit(ctx, a, 1000, 50)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, m.events, "an admission is not an event")

	ok, u, err := i.Admit(ctx, a, 1000, 500)
	require.NoError(t, err)
	require.False(t, ok)
	require.Len(t, m.events, 1)
	require.Equal(t, OpRefuse, m.events[0].Op)
	require.Equal(t, uint64(500), m.events[0].Bytes,
		"a refusal reports what was asked for, since nothing was stored")
	require.Equal(t, a.Instance(), m.events[0].Instance)
	require.Equal(t, uint64(900), u.Used())

	// A capacity of zero is "no ceiling" and refuses nothing.
	m.events = nil
	ok, _, err = i.Admit(ctx, a, 0, 1<<40)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, m.events)
}

// TestNoMeterIsTheOrdinaryCase. Every other test in this package builds an
// index without one.
func TestNoMeterIsTheOrdinaryCase(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	require.NoError(t, i.Add(ctx, a, "peerA", 10))
	require.NoError(t, i.Remove(ctx, a, "peerA", 5))
	ok, _, err := i.Admit(ctx, a, 1, 100)
	require.NoError(t, err)
	require.False(t, ok)
}
