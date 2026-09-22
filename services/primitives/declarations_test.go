package primitives

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesv1 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// TestEVERYPATHTHISSERVICEREADSNAMESANATTRIBUTE.
//
// WHAT ROT LOOKS LIKE IS WHY THIS IS A TEST AND NOT A COMMENT. Every field of a
// queue and of a log is read here as a nested path off the compiled resource,
// and a renamed attribute does not fail: the read misses, the zero value comes
// back, and pkg/queue fills its own default — so an author's visibility
// timeout, attempt budget, backoff and dead letter silently stop taking effect
// with nothing red anywhere. The node-side guard this replaces
// (node/settings_test.go) covered these paths while they were flat keys on a
// grant; it cannot reach them now that they are read off the declaration.
//
// THREE KEYS HAD ALREADY DRIFTED under a comment claiming they were "the
// vocabulary's own paths" — see pkg/tcc/taubyte/v2/schema/attributes.go, which
// is the index this asks.
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

// TestAResourceThisDeploymentDoesNotDeclareIsNotSilentlyAnEmptyOne.
//
// A reader handed an empty page for a log that does not exist believes it is
// caught up; a producer handed success for a queue that does not exist believes
// it sent work nobody will ever run. Both are worse than an error, and both are
// what an "open it if it is there" resolution produces.
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

// TestAnIdThatNamesSomethingElseSaysSo, rather than being reported as a sync
// resource that happens to be empty.
func TestAnIdThatNamesSomethingElseSaysSo(t *testing.T) {
	tier := starting(t).Local()

	err := tier.Send(t.Context(), grantFor(sessionID), "job-1", []byte("work"))
	require.ErrorIs(t, err, ErrNotASync)
}

// TestAQueueReachedAsALogIsTheCallersMistake, and it is told apart from a type
// this service does not serve at all: one says the caller used the wrong
// surface, the other says this cloud has not implemented that primitive.
func TestAQueueReachedAsALogIsTheCallersMistake(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(jobsID), []byte("not a record"))
	require.ErrorIs(t, err, ErrWrongType)

	err = tier.Send(ctx, grantFor(auditID), "job-1", []byte("not an item"))
	require.ErrorIs(t, err, ErrWrongType)
}

// TestAConsensusThisServiceDoesNotKeepIsRefusedByName, for BOTH types.
//
// THE ASYMMETRY IS THE POINT. node/log.go refused a crdt log by name and
// node/queue.go served a crdt queue as raft without a word, so an author who
// wrote the field got the opposite of what it says and the only way to find out
// was to observe the behaviour. A crdt primitive is a different promise —
// append anywhere, an order that converges — not a slower one.
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

// TestAMalformedHandleIsRefusedAtTheDoor. The handle is three segments and the
// separator between them is this service's: a segment carrying one is a
// readable path into somebody else's keyspace, and Spec.Prefix is built out of
// exactly these.
func TestAMalformedHandleIsRefusedAtTheDoor(t *testing.T) {
	tier := starting(t).Local()

	for _, handle := range []string{"", "shop", "shop/prod/id/extra"} {
		err := tier.Send(t.Context(), invoke.Grant{Handle: handle}, "job-1", []byte("work"))
		require.ErrorIs(t, err, ErrNoID, "%q", handle)
	}
}

// TestTheServiceReadsTheDeclarationAndNotTheGrant, which is the difference
// between a service and a proxy. node/queue.go read the visibility timeout, the
// retry budget and the dead letter off the GRANT — the caller's side of the
// conversation — so a worker could have described a different queue.
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

// TestAnInstanceIsRefusedUntilTheVocabularyCanDeclareOne.
//
// THE FIELD IS ON THE WIRE AND NOTHING CAN DESCRIBE WHAT IT NAMES. `instances:`
// is a real block in the vocabulary — its presence is what says a resource
// holds many, and it carries both the validator that admits one and the limit
// on how many there are — and the `sync` kind does not carry it. So an
// instanced queue would be a keyspace no project declared, nobody admitted and
// no limit bounds: any caller could mint them one raft key at a time, and the
// lifecycle pass could resolve not one of them back to a declaration, so none
// of them would ever be retained.
//
// It is reached through the handler because the handle a tier holds is three
// segments and cannot carry an instance at all — the wire is the only door this
// comes through.
func TestAnInstanceIsRefusedUntilTheVocabularyCanDeclareOne(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	api := &handler{h.Service}

	at := func(id string) *primitivesv1.Ref {
		return &primitivesv1.Ref{Project: projectID, Environment: "prod", Id: id, Instance: "eu"}
	}

	_, err := api.QueueSend(ctx, connect.NewRequest(&primitivesv1.QueueSendRequest{
		Ref: at(jobsID), Id: "job-1", Data: []byte("work"),
	}))
	require.ErrorIs(t, err, ErrInstanceUnserved)
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err),
		"a refusal a release fixes is not one to retry")

	_, err = api.LogAppend(ctx, connect.NewRequest(&primitivesv1.LogAppendRequest{
		Ref: at(tracesID), Data: []byte("a record"),
	}))
	require.ErrorIs(t, err, ErrInstanceUnserved)

	_, err = api.LogRead(ctx, connect.NewRequest(&primitivesv1.LogReadRequest{Ref: at(tracesID)}))
	require.ErrorIs(t, err, ErrInstanceUnserved)

	// AND THE RESOURCE ITSELF IS UNTOUCHED. A refusal that had already written
	// somewhere would be the keyspace this one exists to prevent.
	read, err := h.Local().Read(ctx, grantFor(tracesID), "", 0)
	require.NoError(t, err)
	require.Empty(t, read)
}
