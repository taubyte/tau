package kvdb

// Direct unit tests for the pbDelta implementation of the Delta interface
// (delta.go), covering IsEmpty, Size, Marshal/Unmarshal, and the
// dagName/snapshot accessors, none of which were exercised as standalone
// units before (Item H).

import (
	"bytes"
	"testing"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// TestPbDeltaIsEmpty checks every branch of IsEmpty: nil receiver, a delta
// with neither elements nor tombstones, and deltas with only one or the
// other.
func TestPbDeltaIsEmpty(t *testing.T) {
	var nilDelta *pbDelta
	if !nilDelta.IsEmpty() {
		t.Error("a nil *pbDelta should be considered empty")
	}

	empty := &pbDelta{Delta: &pb.Delta{}}
	if !empty.IsEmpty() {
		t.Error("a delta with no elements or tombstones should be empty")
	}

	withElems := &pbDelta{Delta: &pb.Delta{
		Elements: []*pb.Element{{Key: "k", Value: []byte("v")}},
	}}
	if withElems.IsEmpty() {
		t.Error("a delta with elements should not be empty")
	}

	withTombs := &pbDelta{Delta: &pb.Delta{
		Tombstones: []*pb.Element{{Key: "k", Id: "id"}},
	}}
	if withTombs.IsEmpty() {
		t.Error("a delta with tombstones should not be empty")
	}
}

// TestPbDeltaSize checks that Size() is 0 for a nil receiver and grows as
// elements/tombstones are added (proto.Size passthrough).
func TestPbDeltaSize(t *testing.T) {
	var nilDelta *pbDelta
	if s := nilDelta.Size(); s != 0 {
		t.Fatalf("expected 0 size for nil delta, got %d", s)
	}

	empty := &pbDelta{Delta: &pb.Delta{}}
	emptySize := empty.Size()

	populated := &pbDelta{Delta: &pb.Delta{
		Elements: []*pb.Element{{Key: "some-key", Value: []byte("some-value")}},
	}}
	if populated.Size() <= emptySize {
		t.Fatalf("expected populated delta size (%d) to be greater than empty delta size (%d)", populated.Size(), emptySize)
	}
}

// TestPbDeltaMarshalUnmarshal checks the Marshal/Unmarshal round trip, and
// that Marshal on a nil receiver returns (nil, nil) rather than panicking.
func TestPbDeltaMarshalUnmarshal(t *testing.T) {
	var nilDelta *pbDelta
	b, err := nilDelta.Marshal()
	if err != nil {
		t.Fatalf("expected no error marshaling a nil delta, got %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil bytes marshaling a nil delta, got %v", b)
	}

	d := &pbDelta{Delta: &pb.Delta{
		Elements:   []*pb.Element{{Key: "k1", Value: []byte("v1"), Priority: 7}},
		Tombstones: []*pb.Element{{Key: "k2", Id: "id2"}},
		Priority:   42,
		DagName:    "mydag",
		Snapshot:   true,
	}}

	raw, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty marshaled bytes")
	}

	out := &pbDelta{Delta: &pb.Delta{}}
	if err := out.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}

	if out.GetPriority() != 42 {
		t.Errorf("expected priority 42, got %d", out.GetPriority())
	}
	if out.GetDagName() != "mydag" {
		t.Errorf("expected dagName 'mydag', got %q", out.GetDagName())
	}
	if !out.IsSnapshot() {
		t.Error("expected IsSnapshot() true after round trip")
	}
	elems, err := out.GetElements()
	if err != nil {
		t.Fatal(err)
	}
	if len(elems) != 1 || elems[0].GetKey() != "k1" || string(elems[0].GetValue()) != "v1" || elems[0].GetPriority() != 7 {
		t.Errorf("unexpected elements after round trip: %+v", elems)
	}
	tombs, err := out.GetTombstones()
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 1 || tombs[0].GetKey() != "k2" || tombs[0].GetId() != "id2" {
		t.Errorf("unexpected tombstones after round trip: %+v", tombs)
	}
}

// TestPbDeltaDagNameAndSnapshotAccessors checks the SetDagName/GetDagName and
// SetSnapshot/IsSnapshot accessor pairs directly (independent of
// Marshal/Unmarshal).
func TestPbDeltaDagNameAndSnapshotAccessors(t *testing.T) {
	d := &pbDelta{Delta: &pb.Delta{}}

	if d.GetDagName() != "" {
		t.Errorf("expected empty dagName by default, got %q", d.GetDagName())
	}
	d.SetDagName("some-dag")
	if d.GetDagName() != "some-dag" {
		t.Errorf("expected dagName 'some-dag', got %q", d.GetDagName())
	}

	if d.IsSnapshot() {
		t.Error("expected IsSnapshot() false by default")
	}
	d.SetSnapshot(true)
	if !d.IsSnapshot() {
		t.Error("expected IsSnapshot() true after SetSnapshot(true)")
	}
	d.SetSnapshot(false)
	if d.IsSnapshot() {
		t.Error("expected IsSnapshot() false after SetSnapshot(false)")
	}
}

// TestPbDeltaSnapshotMetaAccessors checks the SetSnapshotMeta/SnapshotMeta
// accessor pair (R1) directly, including the zero-value default and a
// Marshal/Unmarshal round trip.
func TestPbDeltaSnapshotMetaAccessors(t *testing.T) {
	d := &pbDelta{Delta: &pb.Delta{}}

	total, id := d.SnapshotMeta()
	if total != 0 || len(id) != 0 {
		t.Errorf("expected zero-value SnapshotMeta by default, got total=%d id=%x", total, id)
	}

	wantID := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	d.SetSnapshotMeta(3, wantID)
	gotTotal, gotID := d.SnapshotMeta()
	if gotTotal != 3 || !bytes.Equal(gotID, wantID) {
		t.Errorf("expected SnapshotMeta (3, %x), got (%d, %x)", wantID, gotTotal, gotID)
	}

	marshaled, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out := &pbDelta{Delta: &pb.Delta{}}
	if err := out.Unmarshal(marshaled); err != nil {
		t.Fatal(err)
	}
	rtTotal, rtID := out.SnapshotMeta()
	if rtTotal != 3 || !bytes.Equal(rtID, wantID) {
		t.Errorf("expected SnapshotMeta round trip (3, %x), got (%d, %x)", wantID, rtTotal, rtID)
	}
}

// TestPbDeltaSetElementsTombstonesPriority checks the plain setter methods
// used throughout the merge path.
func TestPbDeltaSetElementsTombstonesPriority(t *testing.T) {
	d := &pbDelta{Delta: &pb.Delta{}}

	d.SetPriority(99)
	if d.GetPriority() != 99 {
		t.Errorf("expected priority 99, got %d", d.GetPriority())
	}

	elems := []*pb.Element{{Key: "a"}, {Key: "b"}}
	d.SetElements(elems)
	got, err := d.GetElements()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(got))
	}

	tombs := []*pb.Element{{Key: "c", Id: "x"}}
	d.SetTombstones(tombs)
	gotTombs, err := d.GetTombstones()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTombs) != 1 {
		t.Fatalf("expected 1 tombstone, got %d", len(gotTombs))
	}
}
