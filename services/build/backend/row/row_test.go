// The tests are in row_test rather than in row, so that everything they can
// reach is everything a caller can reach: if a filled Op could be assembled
// from outside, it would be assembled here.
package row_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/backend/row"
)

// The two rows there are, through the one way out.
func TestARowIsAKeyAndAVerb(t *testing.T) {
	ops := row.Ops([]row.Op{row.Set("a", []byte("v")), row.Delete("b")})
	require.Equal(t, []raft.BatchOp{
		{Set: &raft.SetCommand{Key: "a", Value: []byte("v")}},
		{Delete: &raft.DeleteCommand{Key: "b"}},
	}, ops)
}

// AND THE ORDER IS THE CALLER'S. An entry writes a record and the rows that
// index it in one batch, and pkg/raft applies a batch in order.
func TestTheRowsComeOutInTheOrderTheyWentIn(t *testing.T) {
	var rows []row.Op
	for _, key := range []string{"a", "b", "c", "d"} {
		rows = append(rows, row.Set(key, nil))
	}

	var keys []string
	for _, op := range row.Ops(rows) {
		keys = append(keys, op.Set.Key)
	}
	require.Equal(t, []string{"a", "b", "c", "d"}, keys)
}

// THE ZERO VALUE IS THE ONLY Op A CALLER CAN WRITE FOR ITSELF, and it names
// nothing: there is no exported field to put a key in. What comes out is the
// one shape raft.Cluster.Batch refuses — neither a Set nor a Delete — so an
// entry carrying one is refused rather than applied, and it could not have been
// about another job in any case.
func TestTheOnlyRowAcallerCanBuildForItselfNamesNothing(t *testing.T) {
	ops := row.Ops([]row.Op{{}})
	require.Len(t, ops, 1)
	require.Nil(t, ops[0].Set)
	require.Nil(t, ops[0].Delete)
}

// An entry with no rows is not a batch of one empty row.
func TestNoRowsIsNoOps(t *testing.T) {
	require.Empty(t, row.Ops(nil))
}

// sink is what keeps the measurement below honest. Store.Batch takes an
// interface's argument, so the slice Ops returns escapes in the real path;
// measured into a local it would not, and the allocation this round added would
// read as zero.
var sink []raft.BatchOp

// WHAT THE ONE WAY OUT COSTS, on the widest entry its caller writes: four rows,
// once per replicated entry. It is printed rather than asserted, because what it
// has to be smaller than is a raft round trip and nothing here can bound one.
func BenchmarkOps(b *testing.B) {
	rows := []row.Op{
		row.Set("build/job/AAA/record", make([]byte, 128)),
		row.Delete("build/index/open/BBB/AAA"),
		row.Set("build/index/terminal/00000000001756000000/AAA", nil),
		row.Set("build/index/ref/BBB/CCC/last", make([]byte, 40)),
	}
	for b.Loop() {
		sink = row.Ops(rows)
	}
	if len(sink) != len(rows) {
		b.Fatal("the compiler elided the thing being measured")
	}
}
