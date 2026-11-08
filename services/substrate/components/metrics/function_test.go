package metrics

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Sorting needs a strict ordering: a node can't be both worse and better than another.
func TestFunctionLessIsStrict(t *testing.T) {
	nodes := []*Function{
		{Cached: 1, AvgRunTime: 10},
		{Cached: 0, AvgRunTime: 1},
		{Cached: 1, ColdStart: 5, Memory: 3},
		{Cached: 0, ColdStart: 9, Memory: 9},
		{Memory: 0.5, Free: 1},
		{Memory: 2, Free: 0.1},
		{Memory: 2, Free: 0.9, Cached: 0.3},
	}
	for _, a := range nodes {
		if a.Less(a) {
			t.Fatalf("%+v is worse than itself", *a)
		}
		for _, b := range nodes {
			if a.Less(b) && b.Less(a) {
				t.Fatalf("%+v and %+v are each worse than the other", *a, *b)
			}
			for _, c := range nodes {
				if a.Less(b) && b.Less(c) && !a.Less(c) {
					t.Fatalf("not transitive: %+v, %+v, %+v", *a, *b, *c)
				}
			}
		}
	}
}

func TestFunctionPrefersFreeCapacity(t *testing.T) {
	full := &Function{Memory: 2, Cached: 1, Free: 0.05}
	idle := &Function{Memory: 2, Cached: 0, Free: 0.9}
	if !full.Less(idle) || idle.Less(full) {
		t.Fatal("the node with free capacity must win even if colder")
	}
}

func TestFunctionSmallLoadDifferenceKeepsWarmNode(t *testing.T) {
	warm := &Function{Memory: 2, Cached: 1, Free: 0.80}
	cold := &Function{Memory: 2, Cached: 0, Free: 0.81}
	if !cold.Less(warm) {
		t.Fatal("one more request in flight must not send work to a cold node")
	}
}

func TestFunctionThatCannotFitLoses(t *testing.T) {
	cannot := &Function{Memory: 0.5, Free: 1}
	can := &Function{Memory: 2, Free: 0.1}
	if !cannot.Less(can) || can.Less(cannot) {
		t.Fatal("a node without the memory for the function must lose")
	}
}

func TestFunctionEncodeRoundTrip(t *testing.T) {
	in := Function{Cached: 1, ColdStart: 2, Memory: 3, AvgRunTime: 4, Free: 0.25}
	var out Function
	if err := out.Decode(in.Encode()); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v", out)
	}
}

// oldEncode is what a node from before the Free field sends.
func oldEncode(m Function) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	binary.Write(&buf, binary.LittleEndian, m.Cached)
	binary.Write(&buf, binary.LittleEndian, m.ColdStart)
	binary.Write(&buf, binary.LittleEndian, m.Memory)
	binary.Write(&buf, binary.LittleEndian, m.AvgRunTime)
	return buf.Bytes()
}

func TestOldNodeWithoutFreeComparesInTheMiddle(t *testing.T) {
	var old Function
	if err := old.Decode(oldEncode(Function{Cached: 1, Memory: 2})); err != nil {
		t.Fatal(err)
	}
	full := &Function{Cached: 1, Memory: 2, Free: 0}
	idle := &Function{Cached: 1, Memory: 2, Free: 1}
	if !full.Less(&old) || !old.Less(idle) {
		t.Fatalf("an old node must sit between a full and an idle one: %+v", old)
	}
}

func TestTruncatedFreeIsAnError(t *testing.T) {
	b := (&Function{Free: 0.5}).Encode()
	if err := new(Function).Decode(b[:len(b)-1]); err == nil {
		t.Fatal("a half-sent field is corrupt, not an old node")
	}
}
