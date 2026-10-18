package storage

import "errors"

// errors.go is this package's sentinel set. It is deliberately NOT a
// connect.NewError code table yet: the RPC surface is a later round, and the
// mapping from these to wire codes belongs beside the handlers that return
// them. What is here is what the logic half can go wrong about, named once so
// no two files spell the same failure differently.
var (
	// ErrEmptyIdentity is the key grammar's one precondition, failed. An empty
	// project, environment, bucket, instance or writer would encode to an
	// empty segment, and go-datastore removes an empty segment when it cleans
	// a key — a removed segment is a MOVED BOUNDARY, which is the class
	// services/auth lost a repository to. See store.go.
	ErrEmptyIdentity = errors.New("storage: an identity segment is empty")

	// ErrNoAddress is a key builder reached through an Address that NewAddress
	// did not make — a zero value, or one assembled by hand. The zero Address
	// builds no key at all rather than a key rooted at "/b////".
	ErrNoAddress = errors.New("storage: address was not built by NewAddress")

	// ErrEmptyObjectName is an object named "". S3 has no such object and
	// neither does this: the name is the identity, and the empty name encodes
	// to an empty segment.
	ErrEmptyObjectName = errors.New("storage: object name is empty")

	// ErrNotThisServicesRow is a key or a row that this package did not write:
	// a segment that is not valid base64url, a stamp that is not 16 hex, a
	// path with the wrong number of segments. It is a corrupt store or a
	// foreign writer, never something to skip quietly.
	ErrNotThisServicesRow = errors.New("storage: row was not written by this service")

	// ErrNotFound is an object, a version or a row that is not there. A
	// tombstoned object is ErrNotFound too — a delete is a row, and resolution
	// reports the object gone rather than reporting the tombstone.
	ErrNotFound = errors.New("storage: not found")

	// ErrRemoteClockSkew is a version stamp whose physical component is
	// further ahead of local time than the skew budget. The record is still
	// valid and must still be merged — it is a CRDT — but the local clock does
	// not advance to it. See clock.go.
	ErrRemoteClockSkew = errors.New("storage: remote stamp is beyond the skew budget")

	// ErrLocalClockSkew is this node's own wall clock having moved backwards
	// past the skew budget relative to the stamps it has already minted. A
	// node in that state refuses to coordinate a write rather than mint a
	// stamp lower than one it already handed out.
	ErrLocalClockSkew = errors.New("storage: local clock has gone backwards past the skew budget")

	// ErrLogicalOverflow is more than 65536 stamps minted in one millisecond
	// by one node. The alternative is wrapping the logical counter, which
	// mints a LOWER stamp than one already returned — a write that silently
	// loses to its own predecessor.
	ErrLogicalOverflow = errors.New("storage: logical clock overflowed within one millisecond")

	// ErrStampRange is a physical millisecond that does not fit in 48 bits, or
	// a time before the epoch.
	ErrStampRange = errors.New("storage: timestamp is out of the packable range")

	// ErrWriterMismatch is a version row whose record names a different writer
	// than its key does. The key is authoritative — it is what makes the row
	// uncontended — so a mismatch is a bug in whoever wrote the row, not a
	// conflict to resolve.
	ErrWriterMismatch = errors.New("storage: version record names a different writer than its key")

	// ErrGateClosed is a garbage-collection gate that did not open. gc.go
	// names which one; a caller that cannot say which gate closed cannot be
	// trusted to delete data.
	ErrGateClosed = errors.New("storage: a deletion gate is closed")
)
