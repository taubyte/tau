package kvdb

import (
	"slices"
	"strings"
	"testing"

	cid "github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/kvdb"
	"gotest.tools/v3/assert"
)

func TestStats(t *testing.T) {
	head1, _ := cid.Parse("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")
	head2, _ := cid.Parse("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyke")

	fakeStats := &stats{heads: []cid.Cid{head1, head2}}

	assert.Equal(t, kvdb.TypeCRDT, fakeStats.Type())
	assert.Assert(t, slices.CompareFunc(fakeStats.Heads(), []cid.Cid{head1, head2}, func(a, b cid.Cid) int {
		return strings.Compare(a.String(), b.String())
	}) == 0)
}
