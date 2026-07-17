package kvdb

import (
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// Delta represent a CRDT changeset, it carries new or updated elements and new or updated tombstones. The priority value allows comparing this update with others, to determine which elements take precedence.
type Delta interface {
	GetElements() ([]*pb.Element, error)
	GetTombstones() ([]*pb.Element, error)
	GetPriority() uint64
	SetElements(elems []*pb.Element)
	SetTombstones(tombs []*pb.Element)
	SetPriority(p uint64)
	Size() int
	GetDagName() string
	SetDagName(string)
	// IsSnapshot reports whether this delta is a compaction snapshot (see
	// Datastore.Compact): its Elements/Tombstones represent the full live
	// state (and carried tombstones) of a named DAG's history up to this
	// point, rather than an incremental change. Snapshot deltas' links are
	// "covered heads" bookkeeping only -- they must never be walked/fetched,
	// since the history they point to may have been purged.
	IsSnapshot() bool
	SetSnapshot(bool)
	// SnapshotMeta returns the compaction-generation metadata carried by a
	// snapshot delta: total is the number of sibling snapshot nodes created
	// by the same Compact() run, and id identifies that generation (see
	// SetSnapshotMeta). Both are zero-valued on non-snapshot deltas and on
	// snapshot deltas produced before this metadata existed (legacy
	// snapshots).
	SnapshotMeta() (total uint32, id []byte)
	// SetSnapshotMeta sets the compaction-generation metadata (see
	// SnapshotMeta).
	SetSnapshotMeta(total uint32, id []byte)
	IsEmpty() bool
	Unmarshal([]byte) error
	Marshal() ([]byte, error)
}

var _ Delta = (*pbDelta)(nil)

type pbDelta struct {
	*pb.Delta
}

func (d *pbDelta) GetElements() ([]*pb.Element, error) {
	return d.Delta.GetElements(), nil
}

func (d *pbDelta) GetTombstones() ([]*pb.Element, error) {
	return d.Delta.GetTombstones(), nil
}

func (d *pbDelta) SetElements(elems []*pb.Element) {
	d.Elements = elems
}

func (d *pbDelta) SetTombstones(tombs []*pb.Element) {
	d.Tombstones = tombs
}

func (d *pbDelta) SetPriority(p uint64) {
	d.Priority = p
}

func (d *pbDelta) Size() int {
	if d == nil {
		return 0
	}
	return proto.Size(d.Delta)
}

func (d *pbDelta) GetDagName() string {
	return d.Delta.GetDagName()
}

func (d *pbDelta) SetDagName(n string) {
	d.DagName = n
}

func (d *pbDelta) IsSnapshot() bool {
	return d.Delta.GetSnapshot()
}

func (d *pbDelta) SetSnapshot(s bool) {
	d.Snapshot = s
}

func (d *pbDelta) SnapshotMeta() (uint32, []byte) {
	return d.Delta.GetSnapshotTotal(), d.Delta.GetSnapshotId()
}

func (d *pbDelta) SetSnapshotMeta(total uint32, id []byte) {
	d.SnapshotTotal = total
	d.SnapshotId = id
}

func (d *pbDelta) IsEmpty() bool {
	return d == nil || (len(d.Tombstones)+len(d.Elements) == 0)
}

func (d *pbDelta) Unmarshal(b []byte) error {
	return proto.Unmarshal(b, d)
}

func (d *pbDelta) Marshal() ([]byte, error) {
	if d != nil {
		return proto.Marshal(d)
	}
	return nil, nil
}
