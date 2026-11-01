package primitives

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestEveryDeclarationPathNamesAnAttribute(t *testing.T) {
	for _, path := range [][]string{
		{queueType, "consensus"},
		{queueType, "visibility_timeout"},
		{queueType, "delay"},
		{queueType, "max_attempts"},
		{queueType, "retry", "interval"},
		{queueType, "retry", "factor"},
		{queueType, "retry", "ceiling"},
		{queueType, "dead_letter", "name"},
		{logType, "consensus"},
		{logType, "retention", "age"},
		{logType, "retention", "size"},
	} {
		key := strings.Join(path, "/")
		require.True(t, schema.IsAttribute(syncKind, key),
			"%s: declarations.go reads it and the vocabulary no longer declares it", key)
	}
}

// declarations_test is the negative half, and it is the half that matters most:
// every refusal here is one a caller could otherwise have mistaken for an empty
// resource.

func TestAResourceThisDeploymentDoesNotDeclareIsNotSilentlyAnEmptyOne(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()
	nobody := grantFor("QmNoSuchResourceIdAnywhereInThisProject00000000")

	_, err := tier.Read(ctx, nobody, "", 0)
	require.ErrorIs(t, err, ErrNotDeclared)

	_, err = tier.Append(ctx, nobody, []byte("into the void"))
	require.ErrorIs(t, err, ErrNotDeclared)

	require.ErrorIs(t, tier.Send(ctx, nobody, "job-1", []byte("work")), ErrNotDeclared)

	_, err = tier.Claim(ctx, nobody, 1)
	require.ErrorIs(t, err, ErrNotDeclared)

	_, err = tier.Depth(ctx, nobody)
	require.ErrorIs(t, err, ErrNotDeclared)
}

func TestAnIdThatNamesSomethingElseSaysSo(t *testing.T) {
	tier := starting(t).Local()

	err := tier.Send(t.Context(), grantFor(sessionID), "job-1", []byte("work"))
	require.ErrorIs(t, err, ErrNotASync)
}

func TestAQueueReachedAsALogIsTheCallersMistake(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(jobsID), []byte("not a record"))
	require.ErrorIs(t, err, ErrWrongType)

	err = tier.Send(ctx, grantFor(auditID), "job-1", []byte("not an item"))
	require.ErrorIs(t, err, ErrWrongType)
}

func TestAConsensusThisServiceDoesNotKeepIsRefusedByName(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(gossipID), []byte("record"))
	require.ErrorIs(t, err, ErrConsensusUnserved)

	err = tier.Send(ctx, grantFor(chatterID), "job-1", []byte("item"))
	require.ErrorIs(t, err, ErrConsensusUnserved)

	// AND IT IS REFUSED ON THE READ SIDE TOO. Serving a crdt log's reads from
	// raft state would answer an author's question with the other answer,
	// quietly, on the path where it is hardest to notice.
	_, err = tier.Read(ctx, grantFor(gossipID), "", 0)
	require.ErrorIs(t, err, ErrConsensusUnserved)
}

func TestAMalformedHandleIsRefusedAtTheDoor(t *testing.T) {
	tier := starting(t).Local()

	for _, handle := range []string{"", "shop", "shop/prod/id/extra"} {
		err := tier.Send(t.Context(), invoke.Grant{Handle: handle}, "job-1", []byte("work"))
		require.ErrorIs(t, err, ErrNoID, "%q", handle)
	}
}

func TestTheServiceReadsTheDeclarationAndNotTheGrant(t *testing.T) {
	h := starting(t)
	ctx := t.Context()

	lying := grantFor(jobsID)
	lying.Settings = map[string]string{
		"type":                     logType,
		"queue/visibility_timeout": "1ns",
		"queue/max_attempts":       "1",
		"queue/consensus":          "crdt",
	}

	require.NoError(t, h.Local().Send(ctx, lying, "job-1", []byte("resize")))
	claimed, err := h.Local().Claim(ctx, lying, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	// The declaration says a minute, so a second later the claim still holds —
	// which the grant's "1ns" would have ended.
	h.clock.advance(time.Second)
	again, err := h.Local().Claim(ctx, lying, 1)
	require.NoError(t, err)
	require.Empty(t, again, "the caller's settings must not have shortened the queue's timeout")
}

func TestAnInstanceIsRefusedUntilTheVocabularyCanDeclareOne(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	api := &handler{h.Service}

	at := func(id string) *primitivesv2.Ref {
		return &primitivesv2.Ref{Project: projectID, Environment: "prod", Id: id, Instance: "eu"}
	}

	_, err := api.QueueSend(ctx, connect.NewRequest(&primitivesv2.QueueSendRequest{
		Ref: at(jobsID), Id: "job-1", Data: []byte("work"),
	}))
	require.ErrorIs(t, err, ErrInstanceUnserved)
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err),
		"a refusal a release fixes is not one to retry")

	_, err = api.LogAppend(ctx, connect.NewRequest(&primitivesv2.LogAppendRequest{
		Ref: at(tracesID), Data: []byte("a record"),
	}))
	require.ErrorIs(t, err, ErrInstanceUnserved)

	_, err = api.LogRead(ctx, connect.NewRequest(&primitivesv2.LogReadRequest{Ref: at(tracesID)}))
	require.ErrorIs(t, err, ErrInstanceUnserved)

	// AND THE RESOURCE ITSELF IS UNTOUCHED. A refusal that had already written
	// somewhere would be the keyspace this one exists to prevent.
	read, err := h.Local().Read(ctx, grantFor(tracesID), "", 0)
	require.NoError(t, err)
	require.Empty(t, read)
}

func TestAQueuesBudgetAndALogsRetentionAreReadOffTheirOwnBlocks(t *testing.T) {
	d := func(v time.Duration) *time.Duration { return &v }
	attempts, factor := int64(7), 1.5
	spec := Spec{Project: "p", Environment: "e", Type: queueType, Name: "q"}

	cfg := deliveryOf(spec, specs.SyncQueue{
		VisibilityTimeout: d(time.Minute), Delay: d(time.Second),
		RetryInterval: d(2 * time.Second), RetryCeiling: d(time.Hour),
		MaxAttempts: &attempts, RetryFactor: &factor, DeadLetterName: "dead",
	})
	require.Equal(t, time.Minute, cfg.VisibilityTimeout)
	require.Equal(t, time.Second, cfg.Delay)
	require.Equal(t, 2*time.Second, cfg.RetryInterval)
	require.Equal(t, time.Hour, cfg.RetryCeiling)
	require.Equal(t, uint32(7), cfg.MaxAttempts)
	require.Equal(t, 1.5, cfg.RetryFactor)
	require.NotEmpty(t, cfg.DeadLetter)

	unwritten := deliveryOf(spec, specs.SyncQueue{})
	require.Zero(t, unwritten.VisibilityTimeout)
	require.Zero(t, unwritten.MaxAttempts)
	require.Empty(t, unwritten.DeadLetter)

	size := int64(1 << 20)
	keep := keepOf(specs.SyncLog{RetentionAge: d(24 * time.Hour), RetentionSize: &size})
	require.Equal(t, 24*time.Hour, keep.age)
	require.Equal(t, size, keep.size)
	require.Equal(t, retention{}, keepOf(specs.SyncLog{}), "absent means forever")

	require.Equal(t, consensusCRDT, consensusIn(specs.Sync{Type: logType, Log: specs.SyncLog{Consensus: consensusCRDT}}))
	require.Equal(t, consensusRaft, consensusIn(specs.Sync{Type: queueType, Queue: specs.SyncQueue{Consensus: consensusRaft}}))
}
